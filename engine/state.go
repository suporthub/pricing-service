package engine

import (
	"database/sql"
	"log"

	_ "github.com/lib/pq"
	"pricing-service/config"
)

type SpreadConfig struct {
	SpreadType string
	Spread     float64
	SpreadPip  float64
}

// ConfigState maps Symbol -> GroupName -> SpreadConfig
var ConfigState = map[string]map[string]SpreadConfig{}

// Cache keys for Redis to avoid allocation in the hot loop
type SymbolKeys struct {
	HSet string
	Pub  string
}
var RedisKeys = map[string]SymbolKeys{}

type DailyStats struct {
	Open float64
	High float64
	Low  float64
	Date string // YYYY-MM-DD
}

// StatsState maps Symbol -> GroupName -> DailyStats
var StatsState = map[string]map[string]*DailyStats{}

func LoadConfigurations(cfg *config.Config) error {
	log.Println("Connecting to PostgreSQL to load spread configurations...")
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return err
	}

	query := `
		SELECT 
			g.name AS group_name, 
			gs.symbol, 
			gs."spreadType", 
			gs.spread, 
			gs."spreadPip" 
		FROM group_symbols gs 
		JOIN groups g ON gs."groupId" = g.id
	`
	rows, err := db.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()

	ConfigState = make(map[string]map[string]SpreadConfig)
	count := 0

	for rows.Next() {
		var groupName, symbol, spreadType string
		var spread, spreadPip float64

		if err := rows.Scan(&groupName, &symbol, &spreadType, &spread, &spreadPip); err != nil {
			log.Printf("Error scanning row: %v", err)
			continue
		}

		if _, exists := ConfigState[symbol]; !exists {
			ConfigState[symbol] = make(map[string]SpreadConfig)
			RedisKeys[symbol] = SymbolKeys{
				HSet: "current_price:" + symbol,
				Pub:  "tick:" + symbol,
			}
		}

		ConfigState[symbol][groupName] = SpreadConfig{
			SpreadType: spreadType,
			Spread:     spread,
			SpreadPip:  spreadPip,
		}
		count++
	}

	log.Printf("Successfully loaded %d spread configurations into RAM.", count)
	return nil
}
