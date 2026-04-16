package publishers

import (
	"context"
	"log"
	"net"
	"strings"

	"github.com/vmihailenco/msgpack/v5"
	"github.com/redis/go-redis/v9"
	"pricing-service/config"
)

type RedisPublisher struct {
	client *redis.ClusterClient
	ctx    context.Context
	script *redis.Script
}

// The Lua script for atomic state update and broadcasting
const luaScript = `
redis.call('HSET', KEYS[1], 'latest', ARGV[1])
redis.call('PUBLISH', ARGV[2], ARGV[1])
return 1
`

func NewRedisPublisher(cfg *config.Config) *RedisPublisher {
	ctx := context.Background()

	// Parse REDIS_NODES
	nodes := strings.Split(cfg.RedisNodes, ",")
	for i, node := range nodes {
		nodes[i] = strings.TrimSpace(node)
	}

	// Build Address Map for pod_ip -> external NodePort
	remap := buildAddressMap(nodes, cfg.RedisPassword)

	// Simple options - rely on user passing the correct nodes pointing to the cluster
	// Intercept the dialer to route internal IPs to external NodePorts
	options := &redis.ClusterOptions{
		Addrs: nodes,
		Password: cfg.RedisPassword,
		Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			origAddr := addr
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
			conn, err := d.DialContext(ctx, network, addr)
			if err != nil {
				log.Printf("Dialer intercepted %s -> %s, but dial failed: %v", origAddr, addr, err)
			}
			return conn, err
		},
	}

	client := redis.NewClusterClient(options)

	// Verify connection
	if err := client.Ping(ctx).Err(); err != nil {
		log.Printf("Warning: Redis Cluster connection failed: %v", err)
	} else {
		log.Printf("Redis Cluster connected successfully via %v", nodes)
	}

	return &RedisPublisher{
		client: client,
		ctx:    ctx,
		script: redis.NewScript(luaScript),
	}
}

func (r *RedisPublisher) PublishFatPayload(symbol string, keyHset string, keyPub string, payload interface{}) {
	data, err := msgpack.Marshal(payload)
	if err != nil {
		return
	}

	// Fire and forget, run lua script with binary data string (Go treats string/[]byte transparently in redis script args)
	err = r.script.Run(r.ctx, r.client, []string{keyHset}, data, keyPub).Err()
	if err != nil {
		log.Printf("Redis Lua execution error for %s: %v", symbol, err)
	}
}

func buildAddressMap(nodes []string, password string) map[string]string {
	remap := make(map[string]string)
	ctx := context.Background()

	for _, node := range nodes {
		client := redis.NewClient(&redis.Options{
			Addr:     node,
			Password: password,
		})

		res, err := client.Do(ctx, "CLIENT", "INFO").Result()
		client.Close()
		if err != nil {
			log.Printf("Auto-discover warning: failed to connect to %s: %v", node, err)
			continue
		}

		info, ok := res.(string)
		if !ok {
			continue // ignore if not string
		}

		parts := strings.Split(info, " ")
		for _, part := range parts {
			if strings.HasPrefix(part, "laddr=") {
				laddr := strings.TrimPrefix(part, "laddr=")
				remap[laddr] = node
				log.Printf("Redis Auto-Map: %s -> %s", laddr, node)
				break
			}
		}
	}
	return remap
}
