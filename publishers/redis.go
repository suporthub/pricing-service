package publishers

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/vmihailenco/msgpack/v5"
	"github.com/redis/go-redis/v9"
	"pricing-service/config"
)


type RedisPublisher struct {
	client *redis.ClusterClient
	ctx    context.Context

	// Lua script for the fat payload (UI layer):
	// Stores the MessagePack blob in HSET field 'latest' and publishes to fat_tick:<SYMBOL>
	// fatScript *redis.Script

	// Lua script for per-group fast-string prices (Go backend):
	// Iterates ARGV pairs [field, value, field, value, ...] into HSET,
	// then publishes the raw "BID,ASK" tick string to tick:<SYMBOL>
	groupScript *redis.Script
}

// luaFatScript stores the full MessagePack blob for the UI layer.
// KEYS[1] = HSET key (e.g. "current_price:EURUSD")
// ARGV[1] = MessagePack binary blob
// ARGV[2] = pub channel (e.g. "fat_tick:EURUSD")
/*
const luaFatScript = `
redis.call('HSET', KEYS[1], 'latest', ARGV[1])
redis.call('PUBLISH', ARGV[2], ARGV[1])
return 1
`
*/

// luaGroupScript writes one HSET field per group ("BID,ASK" string) and
// publishes the full multi-group tick string to the tick channel consumed by Go services.
//
// Payload format on tick:<SYMBOL>:
//   "Raw:1.10010,1.10020|Standard:1.09980,1.10050|VIP:1.09995,1.10035"
//
// KEYS[1] = HSET key (e.g. "current_price:EURUSD")
// ARGV[1] = tick pub channel (e.g. "tick:EURUSD")
// ARGV[2] = pre-built multi-group string (constructed in Go)
// ARGV[3..N] = alternating field/value pairs: groupName, "BID,ASK", groupName, "BID,ASK", ...
const luaGroupScript = `
local tick_channel = ARGV[1]
local tick_payload = ARGV[2]

-- Write each group's "BID,ASK" as a separate hash field
local i = 3
while i <= #ARGV do
    redis.call('HSET', KEYS[1], ARGV[i], ARGV[i+1])
    i = i + 2
end

-- Publish the multi-group string to the tick channel for risk-service
redis.call('PUBLISH', tick_channel, tick_payload)
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

	// Intercept the dialer to route internal IPs to external NodePorts
	options := &redis.ClusterOptions{
		Addrs:    nodes,
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
		client:      client,
		ctx:         ctx,
		// fatScript:   redis.NewScript(luaFatScript),
		groupScript: redis.NewScript(luaGroupScript),
	}
}

// PublishGroupPrices writes per-group fast-string prices into the Redis Hash and
// publishes a multi-group tick string to the tick channel.
//
// Tick payload format (tick:<SYMBOL>):
//   "Raw:1.10010,1.10020|Standard:1.09980,1.10050|VIP:1.09995,1.10035"
//
// This allows the risk-service to parse group-specific bid/ask in ~80ns using
// two string splits — eliminating any need to re-apply spread logic downstream.
//
// Consumers:
//   - execution-service: HGET current_price:<SYMBOL> <GROUP_NAME> → "BID,ASK"
//   - risk-service:      SUBSCRIBE tick:<SYMBOL>  → multi-group string (split by "|" then ":")
//
// groupPrices: map of GroupName → [bid, ask, high, low, pctChange]
func (r *RedisPublisher) PublishGroupPrices(
	symbol string,
	keyHset string,
	tickChannel string,
	groupPrices map[string][]float64,
) {
	// Build the multi-group tick string: "Raw:B,A|Group1:B,A|Group2:B,A"
	// and ARGV pairs for the HSET fields simultaneously in one pass.
	var tickBuf strings.Builder
	argv := []interface{}{tickChannel, ""} // ARGV[2] will be filled after the loop

	first := true
	for groupName, prices := range groupPrices {
		if len(prices) < 5 {
			continue
		}
		// Format: "BID,ASK,HIGH,LOW,PCT"
		pairStr := fmt.Sprintf("%.5f,%.5f,%.5f,%.5f,%.2f", prices[0], prices[1], prices[2], prices[3], prices[4])

		// Append to multi-group tick string
		if !first {
			tickBuf.WriteByte('|')
		}
		tickBuf.WriteString(groupName)
		tickBuf.WriteByte(':')
		tickBuf.WriteString(pairStr)
		first = false

		// HSET field/value pair
		argv = append(argv, groupName, pairStr)
	}

	// Slot the completed tick string into ARGV[2]
	argv[1] = tickBuf.String()

	err := r.groupScript.Run(r.ctx, r.client, []string{keyHset}, argv...).Err()
	if err != nil {
		log.Printf("Redis group prices Lua error for %s: %v", symbol, err)
	}
}

// PublishFatPayload encodes the full group price map as a MessagePack binary blob
// and publishes it to the fat_tick channel for the UI/WebSocket layer (notification-service).
/*
func (r *RedisPublisher) PublishFatPayload(symbol string, keyHset string, fatChannel string, payload interface{}) {
	data, err := msgpack.Marshal(payload)
	if err != nil {
		log.Printf("msgpack marshal error for %s: %v", symbol, err)
		return
	}

	// Fire and forget — binary blob into 'latest' field + fat_tick pub
	err = r.fatScript.Run(r.ctx, r.client, []string{keyHset}, data, fatChannel).Err()
	if err != nil {
		log.Printf("Redis fat payload Lua error for %s: %v", symbol, err)
	}
}
*/

func (r *RedisPublisher) SaveStats(symbol string, group string, open, high, low float64, date string) {
	key := "price_stats:" + symbol
	r.client.HSet(r.ctx, key, map[string]interface{}{
		group + ":open": fmt.Sprintf("%f", open),
		group + ":high": fmt.Sprintf("%f", high),
		group + ":low":  fmt.Sprintf("%f", low),
		group + ":date": date,
	})
	// Set TTL to 48h to clean up old symbols
	r.client.Expire(r.ctx, key, 48*time.Hour)
}

func (r *RedisPublisher) LoadAllStats() (map[string]map[string]interface{}, error) {
	// Find all keys matching price_stats:*
	// Note: In a cluster, we might need to iterate nodes, but for now we'll try a simpler approach
	// since topic creation/config loading is centralized.
	
	// Better: just let the calculator load stats lazily or on startup for known symbols.
	return nil, nil // placeholder, will implement lazy loading in calculator
}

func (r *RedisPublisher) LoadStatsForSymbol(symbol string) (map[string]string, error) {
	return r.client.HGetAll(r.ctx, "price_stats:"+symbol).Result()
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
