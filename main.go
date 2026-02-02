package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"feed-core/internal/config"
	"feed-core/internal/engine"
	"feed-core/internal/ingestion"
	"feed-core/internal/models"
	"feed-core/internal/server"
)

const (
	// Channel buffer sizes
	adapterTickBuffer    = 10000 // Buffer for ticks from adapters to arbitrator
	arbitratorTickBuffer = 10000 // Buffer for ticks from arbitrator to hub
)

func main() {
	// Parse command line flags
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)
	log.Println("========================================")
	log.Println("  Feed Core - Real-Time Market Data    ")
	log.Println("========================================")

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	log.Printf("Configuration loaded:")
	log.Printf("  - WebSocket Port: %d", cfg.Server.Port)
	log.Printf("  - TCP Port: %d", cfg.Server.TCPPort)
	log.Printf("  - Primary Provider: %s", cfg.Settings.PrimaryProvider)
	log.Printf("  - Twelve Data Crypto: %v", cfg.TwelveDataSymbols.Crypto)
	log.Printf("  - Twelve Data Forex: %v", cfg.TwelveDataSymbols.Forex)
	log.Printf("  - Massive Crypto: %v", cfg.MassiveSymbols.Crypto)
	log.Printf("  - Massive Forex: %v", cfg.MassiveSymbols.Forex)
	log.Printf("  - Silence Threshold: %dms", cfg.Settings.PrimarySilenceThresholdMS)

	// Create channels for data pipeline
	// Adapters -> Arbitrator -> Hub
	adapterChan := make(chan *models.Tick, adapterTickBuffer)
	hubChan := make(chan *models.Tick, arbitratorTickBuffer)

	// Initialize components
	log.Println("Initializing components...")

	// 1. Create the Hub (broadcasts to all clients)
	hub := engine.NewHub(hubChan)
	hub.Start()

	// 2. Create the Arbitrator (failover logic)
	arbitrator := engine.NewArbitrator(cfg, adapterChan, hubChan)
	arbitrator.Start()

	// 3. Create and start the Twelve Data adapter (Primary)
	twelveAdapter := ingestion.NewTwelveAdapter(cfg, adapterChan)
	if err := twelveAdapter.Start(); err != nil {
		log.Printf("Warning: Failed to start Twelve Data adapter: %v", err)
		log.Println("Continuing without primary data source...")
	} else {
		log.Println("Twelve Data adapter started (Primary)")
	}

	// 4. Create and start the Massive adapter (Secondary)
	massiveAdapter := ingestion.NewMassiveAdapter(cfg, adapterChan)
	if err := massiveAdapter.Start(); err != nil {
		log.Printf("Warning: Failed to start Massive adapter: %v", err)
		log.Println("Continuing without secondary data source...")
	} else {
		log.Println("Massive adapter started (Secondary)")
	}

	// 5. Create and start the WebSocket server (Public Gateway)
	wsServer := server.NewWebSocketServer(hub, cfg.Server.Port)
	if err := wsServer.Start(); err != nil {
		log.Fatalf("Failed to start WebSocket server: %v", err)
	}
	log.Printf("WebSocket server started on port %d", cfg.Server.Port)

	// 6. Create and start the TCP server (Trading Engine dedicated)
	tcpServer := server.NewTCPServer(hub, cfg.Server.TCPPort)
	if err := tcpServer.Start(); err != nil {
		log.Fatalf("Failed to start TCP server: %v", err)
	}
	log.Printf("TCP server started on port %d (Trading Engine)", cfg.Server.TCPPort)

	log.Println("========================================")
	log.Println("  Feed Core is now running!            ")
	log.Println("  Press Ctrl+C to shutdown             ")
	log.Println("========================================")

	// Start metrics logger
	go logMetrics(hub, arbitrator)

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("\nShutdown signal received, stopping services...")

	// Graceful shutdown (reverse order of startup)
	tcpServer.Stop()
	wsServer.Stop()
	massiveAdapter.Stop()
	twelveAdapter.Stop()
	arbitrator.Stop()
	hub.Stop()

	// Close channels
	close(adapterChan)
	close(hubChan)

	log.Println("Feed Core shutdown complete")
}

// logMetrics periodically logs system metrics
func logMetrics(hub *engine.Hub, arbitrator *engine.Arbitrator) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		stats := arbitrator.GetStats()
		log.Printf("[Metrics] Clients: %d | Ticks: %d | Primary: %d | Backup: %d | Failovers: %d | Dropped: %d",
			hub.ClientCount(),
			hub.TotalTicks(),
			stats.PrimaryTicksProcessed,
			stats.BackupTicksProcessed,
			stats.FailoversTriggered,
			stats.BackupTicksDropped,
		)
	}
}
