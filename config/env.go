package config

import (
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	DatabaseURL       string
	RedisNodes        string
	RedisPassword     string
	RedisExternalHost string
	RedisAddressMap   string
	ZmqBind           string
	FixCfgPath        string
}

func LoadConfig() *Config {
	_ = godotenv.Load(".env") // ignore error if no .env file

	return &Config{
		DatabaseURL:       getEnv("DATABASE_URL", "postgres://livefxhubv3:Livefxhub%40123@10.10.0.1:30432/user_db?sslmode=disable"),
		RedisNodes:        getEnv("REDIS_NODES", "185.131.54.146:31010,185.131.54.146:31011,185.131.54.146:31003,185.131.54.146:31009,185.131.54.146:31007,185.131.54.146:31008"),
		RedisPassword:     getEnv("REDIS_PASSWORD", ""),
		RedisExternalHost: getEnv("REDIS_EXTERNAL_HOST", "185.131.54.146"),
		RedisAddressMap:   getEnv("REDIS_ADDRESS_MAP", ""), // e.g. "10.0.0.5:31001"
		ZmqBind:           getEnv("ZMQ_BIND", "tcp://0.0.0.0:5556"),
		FixCfgPath:        getEnv("FIX_CFG_PATH", "./config/quickfix.cfg"),
	}
}

func getEnv(key string, fallback string) string {
	if val, exists := os.LookupEnv(key); exists {
		return val
	}
	return fallback
}
