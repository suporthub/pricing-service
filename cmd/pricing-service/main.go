package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	
	"pricing-service/config"
	"pricing-service/engine"
	"pricing-service/market"
	"pricing-service/publishers"

	"github.com/quickfixgo/quickfix"
	"github.com/quickfixgo/quickfix/log/screen"
)

func main() {
	log.Println("Starting livefxhub Pricing Engine...")

	// 1. Load Configurations
	cfg := config.LoadConfig()

	err := engine.LoadConfigurations(cfg)
	if err != nil {
		log.Fatalf("Fatal: Failed to load spread configurations: %v", err)
	}

	// 2. Initialize Publishers
	redisPub := publishers.NewRedisPublisher(cfg)
	zmqPub := publishers.NewZMQPublisher(cfg.ZmqBind)

	// 3. Initialize Thread 2 (Math Worker)
	calc := engine.NewCalculator(redisPub, zmqPub)
	go calc.StartWorker()

	// 4. Initialize Thread 1 (FIX Listener)
	cfgFile, err := os.Open(cfg.FixCfgPath)
	if err != nil {
		log.Fatalf("Error opening %s, %v\n", cfg.FixCfgPath, err)
	}
	defer cfgFile.Close()

	appSettings, err := quickfix.ParseSettings(cfgFile)
	if err != nil {
		log.Fatalf("Error reading cfg: %v\n", err)
	}

	var username, password string
	for _, dict := range appSettings.SessionSettings() {
		username, _ = dict.Setting("Username")
		password, _ = dict.Setting("Password")
		break // just grab the first session's credentials
	}

	app := market.NewFIXApplication(calc, username, password)
	storeFactory := quickfix.NewMemoryStoreFactory()
	logFactory := screen.NewLogFactory()

	initiator, err := quickfix.NewInitiator(app, storeFactory, appSettings, logFactory)
	if err != nil {
		log.Fatalf("Unable to create Initiator: %v\n", err)
	}

	// 5. Start and block
	err = initiator.Start()
	if err != nil {
		log.Fatalf("Unable to start Initiator: %v\n", err)
	}

	log.Println("Pricing Engine running. Press Ctrl+C to stop.")
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	<-interrupt

	initiator.Stop()
	log.Println("Pricing Engine shut down gracefully.")
}
