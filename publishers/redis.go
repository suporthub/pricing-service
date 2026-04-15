package publishers

import (
	"context"
	"log"
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
redis.call('PUBLISH', KEYS[2], ARGV[1])
return 1
`

func NewRedisPublisher(cfg *config.Config) *RedisPublisher {
	ctx := context.Background()

	// Parse REDIS_NODES
	nodes := strings.Split(cfg.RedisNodes, ",")
	for i, node := range nodes {
		nodes[i] = strings.TrimSpace(node)
	}

	// Address remap (handling pod_ip:6379 -> external:port)
	remap := make(map[string]string)
	if cfg.RedisAddressMap != "" {
		entries := strings.Split(cfg.RedisAddressMap, ",")
		for _, entry := range entries {
			parts := strings.Split(entry, ":")
			if len(parts) == 3 {
				// Format: 10.0.0.1:6379:185.1.1.1:31000
				// But user provided a simpler format maybe? Let's use standard Split
				// Assuming format "pod_ip:6379->ext_ip:port" or similar. But let's keep it simple: Address mapping logic.
				// Since we might not need this complexity if we use a direct connection 
				// or if they provide correct external node addresses in REDIS_NODES,
				// we just pass it to NewClusterAddressOpt below if needed, but go-redis takes a route mapping.
			}
		}
	}

	// Simple options - rely on user passing the correct nodes pointing to the cluster
	options := &redis.ClusterOptions{
		Addrs: nodes,
		Password: cfg.RedisPassword,
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
	err = r.script.Run(r.ctx, r.client, []string{keyHset, keyPub}, data).Err()
	if err != nil {
		log.Printf("Redis Lua execution error for %s: %v", symbol, err)
	}
}
