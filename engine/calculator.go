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
	
	// Always store the raw, unmodified Bid/Ask
	fatPayload["Raw"] = []float64{tick.Bid, tick.Ask}

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

			fatPayload[groupName] = []float64{newBid, newAsk}
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

	c.redisPub.PublishFatPayload(tick.Symbol, keys.HSet, keys.Pub, fatPayload)
}
