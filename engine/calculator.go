package engine

import (
	"log"
	"math"
	"pricing-service/publishers"
	"strconv"
        "strings"
	"time"
)

type RawTick struct {
	Symbol string
	Bid    float64
	Ask    float64
}

type Calculator struct {
	TickPipe  chan RawTick
	redisPub  *publishers.RedisPublisher
	zmqPub    *publishers.ZMQPublisher
}

func NewCalculator(redisPub *publishers.RedisPublisher, zmqPub *publishers.ZMQPublisher) *Calculator {
	return &Calculator{
		TickPipe: make(chan RawTick, 10000),
		redisPub: redisPub,
		zmqPub:   zmqPub,
	}
}

func (c *Calculator) StartWorker() {
	log.Println("Starting Math Worker Goroutine...")
	for tick := range c.TickPipe {
		c.processTick(tick)
	}
}

func (c *Calculator) processTick(tick RawTick) {
	// 1. ZMQ Publisher (Raw fallback / backward compatibility format)
	// Some systems might consume directly from ZMQ
	c.zmqPub.Publish(tick.Symbol, tick.Ask, tick.Bid)

	// 2. Redis Fat Payload (The new architecture)
	groupSpreads, exists := ConfigState[tick.Symbol]
	
	fatPayload := make(map[string][]float64)

	// If symbol has spread configurations in the DB, calculate and add them
	if exists {
		mid := (tick.Ask + tick.Bid) / 2.0

		for groupName, config := range groupSpreads {
			halfSpread := (config.Spread * config.SpreadPip) / 2.0

			var newBid, newAsk float64

			if config.SpreadType == "fixed" {
				newAsk = mid + halfSpread
				newBid = mid - halfSpread
			} else { // "variable" or default
				newAsk = tick.Ask + halfSpread
				newBid = tick.Bid - halfSpread
			}

			// Ensure bid is never greater than ask due to huge negative spreads
			if newBid >= newAsk {
				newBid = newAsk - (config.SpreadPip * 0.1) // minimal safety gap
			}

			fatPayload[groupName] = c.updateAndGetStats(tick.Symbol, groupName, newBid, newAsk)
		}
	}

	// Also update "Raw" stats
	rawBid, rawAsk := tick.Bid, tick.Ask
	fatPayload["Raw"] = c.updateAndGetStats(tick.Symbol, "Raw", rawBid, rawAsk)

	keys, keyExists := RedisKeys[tick.Symbol]
	if !keyExists {
		// Generate keys dynamically if the symbol wasn't in the DB config
		keys = SymbolKeys{
			HSet: "current_price:" + tick.Symbol,
			Pub:  "tick:" + tick.Symbol,
		}
	}

	// 3. Publish per-group fast-string prices to Redis Hash + tick channel.
	//    Format in HSET: current_price:<SYMBOL>  field=<GROUP>  value="BID,ASK"
	//    Format on tick:<SYMBOL> pub/sub:
	//      "Raw:1.10010,1.10020|Standard:1.09980,1.10050|VIP:1.09995,1.10035"
	//    The risk-service splits this string by "|" then ":" to get per-group prices
	//    with zero spread re-calculation.
	c.redisPub.PublishGroupPrices(
		tick.Symbol,
		keys.HSet,
		keys.Pub, // "tick:<SYMBOL>"
		fatPayload,
	)

	// 4. Publish MessagePack fat payload to the UI/WebSocket channel.
	//    This is for the notification-service / frontend terminal.
	//    Channel: "fat_tick:<SYMBOL>"
	// fatChannel := "fat_tick:" + tick.Symbol
	// c.redisPub.PublishFatPayload(tick.Symbol, keys.HSet, fatChannel, fatPayload)
}


func (c *Calculator) updateAndGetStats(symbol string, group string, bid, ask float64) []float64 {
	now := time.Now().UTC()
	today := now.Format("2006-01-02")

	// Ensure symbol map exists
	if _, ok := StatsState[symbol]; !ok {
		StatsState[symbol] = make(map[string]*DailyStats)
		// Lazy load from Redis
		c.lazyLoadStats(symbol)
	}

	stats, ok := StatsState[symbol][group]
	if !ok || stats.Date != today {
		// New day or new group: Initialize
		if !ok {
			stats = &DailyStats{
				Open: ask,
				High: ask,
				Low:  ask,
				Date: today,
			}
			StatsState[symbol][group] = stats
		} else {
			// Day change
			stats.Open = ask
			stats.High = ask
			stats.Low = ask
			stats.Date = today
		}
		c.redisPub.SaveStats(symbol, group, stats.Open, stats.High, stats.Low, stats.Date)
	} else {
		// Update existing stats
		changed := false
		if ask > stats.High {
			stats.High = ask
			changed = true
		}
		if ask < stats.Low {
			stats.Low = ask
			changed = true
		}
		if changed {
			c.redisPub.SaveStats(symbol, group, stats.Open, stats.High, stats.Low, stats.Date)
		}
	}

	pctChange := 0.0
	if stats.Open != 0 {
		pctChange = ((ask - stats.Open) / stats.Open) * 100.0
	}

	// Keep precision to a reasonable level (e.g. 2 decimal places for pct, 5-6 for prices)
	return []float64{
		bid,
		ask,
		stats.High,
		stats.Low,
		math.Round(pctChange*100) / 100, // Round to 2 decimal places
	}
}

func (c *Calculator) lazyLoadStats(symbol string) {
	redisData, err := c.redisPub.LoadStatsForSymbol(symbol)
	if err != nil || len(redisData) == 0 {
		return
	}

	today := time.Now().UTC().Format("2006-01-02")

	for key, val := range redisData {
		parts := strings.Split(key, ":")
		if len(parts) != 2 {
			continue
		}
		groupName := parts[0]
		field := parts[1]

		if _, ok := StatsState[symbol][groupName]; !ok {
			StatsState[symbol][groupName] = &DailyStats{Date: today}
		}
		stats := StatsState[symbol][groupName]

		fval, _ := strconv.ParseFloat(val, 64)
		switch field {
		case "open":
			stats.Open = fval
		case "high":
			stats.High = fval
		case "low":
			stats.Low = fval
		case "date":
			stats.Date = val
		}
	}
}
