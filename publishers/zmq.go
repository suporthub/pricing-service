package publishers

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	zmq "github.com/pebbe/zmq4"
)

type ZMQPublisher struct {
	addr   string
	socket *zmq.Socket
	mu     sync.Mutex
}

func NewZMQPublisher(addr string) *ZMQPublisher {
	pub := &ZMQPublisher{addr: addr}
	pub.connect()
	return pub
}

func (z *ZMQPublisher) connect() {
	z.mu.Lock()
	defer z.mu.Unlock()

	if z.socket != nil {
		z.socket.Close()
	}

	soc, err := zmq.NewSocket(zmq.PUB)
	if err != nil {
		log.Fatalf("FAILED to create ZMQ socket: %v", err)
	}

	err = soc.SetSndhwm(0) // Unlimited buffer for fast writes
	if err != nil {
		log.Printf("ZMQ SetSndhwm error: %v", err)
	}

	err = soc.Bind(z.addr)
	if err != nil {
		log.Fatalf("FAILED to bind ZMQ to %s: %v", z.addr, err)
	}

	z.socket = soc
	log.Printf("ZeroMQ Publisher bound to %s", z.addr)
}

func (z *ZMQPublisher) Publish(symbol string, ask float64, bid float64) {
	// Replicating Python structure:
	// {"datafeeds/SYMBOL/o": bid, "datafeeds/SYMBOL/b": ask}
	topicAsk := "datafeeds/" + symbol + "/b"
	topicBid := "datafeeds/" + symbol + "/o"

	payload := map[string]float64{
		topicAsk: ask,
		topicBid: bid,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return
	}

	z.mu.Lock()
	defer z.mu.Unlock()

	_, err = z.socket.SendMessageDontwait(string(data))
	if err != nil {
		// Log and swallow non-blocking error, or try blocking
		go func(msg string) {
			time.Sleep(100 * time.Millisecond)
			z.mu.Lock()
			_, _ = z.socket.SendMessage(msg)
			z.mu.Unlock()
		}(string(data))
	}
}
