package publishers

import (
	"encoding/json"
	"log"
	"sync"

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
	// Replicating Python structure exactly:
	// zmq_publisher.send_json({"datafeeds/SYMBOL/o": bid, "datafeeds/SYMBOL/b": ask})
	// Python send_json = single frame JSON string
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

	// Use Send (single frame) to match Python's send_json format.
	// Python subscribers use recv_json() which expects a single frame.
	_, err = z.socket.Send(string(data), zmq.DONTWAIT)
	if err != nil {
		// Retry once with blocking send
		_, _ = z.socket.Send(string(data), 0)
	}
}
