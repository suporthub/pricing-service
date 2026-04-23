package engine

import (
	"log"
	"pricing-service/publishers"
)

type RawTick struct {
	Symbol string
	Bid    float64
	Ask    float64
}

type Calculator struct {
	TickPipe chan RawTick
	redisPub *publishers.RedisPublisher
	zmqPub   *publishers.ZMQPublisher
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
	c.zmqPub.Publish(tick.Symbol, tick.Ask, tick.Bid)

	// 2. Build the group price map (used by both Redis publish calls below)
	groupSpreads, exists := ConfigState[tick.Symbol]

	groupPrices := make(map[string][]float64)

	// Always include the raw, unmodified Bid/Ask as the "Raw" group
	groupPrices["Raw"] = []float64{tick.Bid, tick.Ask}

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

			// Ensure bid is never greater than ask due to large negative spreads
			if newBid >= newAsk {
				newBid = newAsk - (config.SpreadPip * 0.1) // minimal safety gap
			}

			groupPrices[groupName] = []float64{newBid, newAsk}
		}
	}

	keys, keyExists := RedisKeys[tick.Symbol]
	if !keyExists {
		// Generate keys dynamically if the symbol wasn't in the DB config
		keys = SymbolKeys{
			HSet: "current_price:" + tick.Symbol,
			Pub:  "tick:" + tick.Symbol,
		}
	}

	// 3. Publish per-group fast-string prices to Redis Hash + tick channel.
	//    This is the format consumed by execution-service (HGET) and risk-service (SUBSCRIBE).
	//    Format: HSET current_price:<SYMBOL> <GROUP> "BID,ASK"
	//            PUBLISH tick:<SYMBOL> "BID,ASK"
	c.redisPub.PublishGroupPrices(
		tick.Symbol,
		keys.HSet,
		keys.Pub, // "tick:<SYMBOL>"
		tick.Bid,
		tick.Ask,
		groupPrices,
	)

	// 4. Publish MessagePack fat payload to the UI/WebSocket channel.
	//    This is for the notification-service / frontend terminal.
	//    Channel: "fat_tick:<SYMBOL>"
	fatChannel := "fat_tick:" + tick.Symbol
	c.redisPub.PublishFatPayload(tick.Symbol, keys.HSet, fatChannel, groupPrices)
}
