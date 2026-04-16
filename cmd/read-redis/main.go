package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"github.com/redis/go-redis/v9"
	"github.com/vmihailenco/msgpack/v5"
	"pricing-service/config"
)

func main() {
	cfg := config.LoadConfig()
	ctx := context.Background()

	nodes := strings.Split(cfg.RedisNodes, ",")
	for i, node := range nodes {
		nodes[i] = strings.TrimSpace(node)
	}

	// Build address remap (same logic as publishers/redis.go)
	remap := make(map[string]string)
	for _, node := range nodes {
		client := redis.NewClient(&redis.Options{
			Addr:     node,
			Password: cfg.RedisPassword,
		})
		res, err := client.Do(ctx, "CLIENT", "INFO").Result()
		client.Close()
		if err != nil {
			continue
		}
		info, ok := res.(string)
		if !ok {
			continue
		}
		for _, part := range strings.Split(info, " ") {
			if strings.HasPrefix(part, "laddr=") {
				laddr := strings.TrimPrefix(part, "laddr=")
				remap[laddr] = node
				break
			}
		}
	}

	client := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs:    nodes,
		Password: cfg.RedisPassword,
		Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if newAddr, ok := remap[addr]; ok {
				addr = newAddr
			} else {
				addrIP := strings.Split(addr, ":")[0]
				for laddr, extNode := range remap {
					if strings.HasPrefix(laddr, addrIP+":") {
						addr = extNode
						break
					}
				}
			}
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	})

	// Which symbol to read (default XAUUSD or from args)
	symbol := "XAUUSD"
	if len(os.Args) > 1 {
		symbol = os.Args[1]
	}

	// Read the HSET value
	key := "current_price:" + symbol
	data, err := client.HGet(ctx, key, "latest").Bytes()
	if err != nil {
		log.Fatalf("Failed to read %s: %v", key, err)
	}

	// Decode msgpack
	var payload map[string][]float64
	err = msgpack.Unmarshal(data, &payload)
	if err != nil {
		log.Fatalf("Failed to decode msgpack: %v", err)
	}

	fmt.Printf("=== %s ===\n", key)
	for group, prices := range payload {
		fmt.Printf("  %-20s  Bid: %.5f   Ask: %.5f\n", group, prices[0], prices[1])
	}

	// Also list all current_price keys across the cluster
	fmt.Println("\n=== All current_price keys across cluster ===")
	var cursor uint64
	allKeys := []string{}
	for {
		var keys []string
		keys, cursor, err = client.Scan(ctx, cursor, "current_price:*", 100).Result()
		if err != nil {
			break
		}
		allKeys = append(allKeys, keys...)
		if cursor == 0 {
			break
		}
	}
	fmt.Printf("Total symbols in cluster: %d\n", len(allKeys))
	for i, k := range allKeys {
		fmt.Printf("  %2d) %s\n", i+1, k)
	}
}
