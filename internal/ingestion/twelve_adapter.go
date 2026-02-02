package ingestion

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"feed-core/internal/config"
	"feed-core/internal/models"

	"github.com/gorilla/websocket"
)

const (
	twelveDataWSURL      = "wss://ws.twelvedata.com/v1/quotes/price"
	twelveMaxSymbolBatch = 50 // Max symbols per subscribe message
	twelveReadTimeout    = 30 * time.Second
	twelvePingInterval   = 10 * time.Second
)

// TwelveDataMessage represents an incoming message from Twelve Data
// Based on official docs: https://twelvedata.com/docs#ws-real-time-price
type TwelveDataMessage struct {
	Event     string  `json:"event"`      // "price", "heartbeat", "subscribe-status"
	Symbol    string  `json:"symbol"`     // e.g., "BTC/USD"
	Currency  string  `json:"currency"`   // Currency code (e.g., "USD")
	Exchange  string  `json:"exchange"`   // Exchange name
	Type      string  `json:"type"`       // Instrument type (e.g., "Physical Currency", "Common Stock")
	Price     float64 `json:"price"`      // Real-time price
	Bid       float64 `json:"bid"`        // Bid price (where available)
	Ask       float64 `json:"ask"`        // Ask price (where available)
	DayVolume int64   `json:"day_volume"` // Volume for current trading day
	Timestamp int64   `json:"timestamp"`  // Unix timestamp in seconds
	Status    string  `json:"status"`     // For subscribe-status events ("ok", etc.)
	Code      int     `json:"code"`       // Error code if any
	Message   string  `json:"message"`    // Error/status message
}

// TwelveSubscribeRequest is the subscribe command format
// According to Twelve Data docs: {"action": "subscribe", "params": {"symbols": "AAPL,EUR/USD,BTC/USD"}}
type TwelveSubscribeRequest struct {
	Action string       `json:"action"` // "subscribe" or "unsubscribe"
	Params TwelveParams `json:"params"`
}

// TwelveParams for subscribe request - symbols is a comma-separated string per docs
type TwelveParams struct {
	Symbols string `json:"symbols"` // Comma-separated: "BTC/USD,EUR/USD"
}

// TwelveHeartbeat is sent to keep connection alive
type TwelveHeartbeat struct {
	Action string `json:"action"` // "heartbeat"
}

// TwelveAdapter connects to Twelve Data WebSocket and ingests price data
type TwelveAdapter struct {
	cfg          *config.Config
	conn         *websocket.Conn
	tickChan     chan<- *models.Tick
	stopChan     chan struct{}
	reconnecting bool
	mu           sync.RWMutex
	connected    bool
	symbols      []string // Internal format symbols
}

// NewTwelveAdapter creates a new Twelve Data adapter
func NewTwelveAdapter(cfg *config.Config, tickChan chan<- *models.Tick) *TwelveAdapter {
	return &TwelveAdapter{
		cfg:      cfg,
		tickChan: tickChan,
		stopChan: make(chan struct{}),
		symbols:  cfg.AllTwelveDataSymbols(),
	}
}

// Start begins the connection and data ingestion
func (t *TwelveAdapter) Start() error {
	if err := t.connect(); err != nil {
		return err
	}

	go t.readLoop()
	go t.pingLoop()

	return nil
}

// Stop gracefully shuts down the adapter
func (t *TwelveAdapter) Stop() {
	close(t.stopChan)
	t.mu.Lock()
	if t.conn != nil {
		t.conn.Close()
	}
	t.connected = false
	t.mu.Unlock()
}

// IsConnected returns the current connection status
func (t *TwelveAdapter) IsConnected() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.connected
}

// connect establishes WebSocket connection to Twelve Data
func (t *TwelveAdapter) connect() error {
	url := fmt.Sprintf("%s?apikey=%s", twelveDataWSURL, t.cfg.Credentials.TwelveData)

	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("twelve data connection failed: %w", err)
	}

	t.mu.Lock()
	t.conn = conn
	t.connected = true
	t.mu.Unlock()

	log.Println("[TwelveData] Connected to WebSocket")

	// Subscribe to symbols in batches
	if err := t.subscribe(); err != nil {
		t.conn.Close()
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	return nil
}

// subscribe sends subscription requests for all symbols (batched if needed)
// Per Twelve Data docs: symbols must be comma-separated string
func (t *TwelveAdapter) subscribe() error {
	twelveSymbols := t.cfg.GetTwelveSymbols()

	// Batch symbols to avoid exceeding frame limits (message size < 1MB)
	for i := 0; i < len(twelveSymbols); i += twelveMaxSymbolBatch {
		end := i + twelveMaxSymbolBatch
		if end > len(twelveSymbols) {
			end = len(twelveSymbols)
		}

		batch := twelveSymbols[i:end]

		// Join symbols as comma-separated string per Twelve Data docs
		symbolsStr := strings.Join(batch, ",")

		req := TwelveSubscribeRequest{
			Action: "subscribe",
			Params: TwelveParams{
				Symbols: symbolsStr,
			},
		}

		t.mu.RLock()
		conn := t.conn
		t.mu.RUnlock()

		if err := conn.WriteJSON(req); err != nil {
			return fmt.Errorf("subscribe failed for batch %d: %w", i/twelveMaxSymbolBatch, err)
		}

		log.Printf("[TwelveData] Subscribed to symbols: %s (batch %d)", symbolsStr, i/twelveMaxSymbolBatch+1)
	}

	return nil
}

// readLoop continuously reads messages from the WebSocket
func (t *TwelveAdapter) readLoop() {
	defer func() {
		t.mu.Lock()
		t.connected = false
		t.mu.Unlock()
	}()

	for {
		select {
		case <-t.stopChan:
			return
		default:
		}

		t.mu.RLock()
		conn := t.conn
		t.mu.RUnlock()

		if conn == nil {
			t.handleReconnect()
			continue
		}

		conn.SetReadDeadline(time.Now().Add(twelveReadTimeout))

		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("[TwelveData] Read error: %v", err)
			t.handleReconnect()
			continue
		}

		t.processMessage(message)
	}
}

// TwelveSubscribeStatus represents the subscribe-status response
type TwelveSubscribeStatus struct {
	Event   string                   `json:"event"`
	Status  string                   `json:"status"`
	Success []TwelveSubscribeSuccess `json:"success"`
	Fails   []TwelveSubscribeFail    `json:"fails"`
}

// TwelveSubscribeSuccess represents a successful subscription
type TwelveSubscribeSuccess struct {
	Symbol   string `json:"symbol"`
	Exchange string `json:"exchange"`
	Country  string `json:"country"`
	Type     string `json:"type"`
}

// TwelveSubscribeFail represents a failed subscription
type TwelveSubscribeFail struct {
	Symbol  string `json:"symbol"`
	Message string `json:"message"`
}

// processMessage parses and handles incoming WebSocket messages
func (t *TwelveAdapter) processMessage(data []byte) {
	// First, try to determine the event type
	var baseMsg struct {
		Event string `json:"event"`
	}
	if err := json.Unmarshal(data, &baseMsg); err != nil {
		log.Printf("[TwelveData] Parse error: %v, data: %s", err, string(data))
		return
	}

	switch baseMsg.Event {
	case "price":
		var msg TwelveDataMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("[TwelveData] Price parse error: %v", err)
			return
		}
		t.handlePriceTick(msg)

	case "heartbeat":
		// Heartbeat response received, connection is healthy
		log.Println("[TwelveData] Heartbeat acknowledged")

	case "subscribe-status":
		var status TwelveSubscribeStatus
		if err := json.Unmarshal(data, &status); err != nil {
			log.Printf("[TwelveData] Subscribe status parse error: %v", err)
			return
		}
		// Log successful subscriptions
		if len(status.Success) > 0 {
			for _, s := range status.Success {
				log.Printf("[TwelveData] ✓ Subscribed: %s (%s, %s)", s.Symbol, s.Exchange, s.Type)
			}
		}
		// Log failed subscriptions
		if len(status.Fails) > 0 {
			for _, f := range status.Fails {
				log.Printf("[TwelveData] ✗ Subscribe failed: %s - %s", f.Symbol, f.Message)
			}
		}
		// Log overall status
		if status.Status != "" && status.Status != "ok" {
			log.Printf("[TwelveData] Subscribe status: %s", status.Status)
		}

	case "":
		// Empty event - log raw data for debugging
		log.Printf("[TwelveData] Empty event, raw data: %s", string(data[:min(200, len(data))]))

	default:
		// Unknown event type, log for debugging
		log.Printf("[TwelveData] Unknown event: %s, data: %s", baseMsg.Event, string(data[:min(200, len(data))]))
	}
}

// handlePriceTick converts Twelve Data price to internal Tick format
func (t *TwelveAdapter) handlePriceTick(msg TwelveDataMessage) {
	// Normalize symbol from "BTC/USD" to "BTC-USD"
	normalizedSymbol := config.NormalizeFromTwelve(msg.Symbol)

	// Determine asset type based on Twelve Data's type field or config
	assetType := models.Crypto
	if msg.Type == "Physical Currency" {
		// Forex pairs are "Physical Currency" in Twelve Data
		if t.cfg.IsTwelveDataForex(normalizedSymbol) {
			assetType = models.Forex
		}
	} else if t.cfg.IsTwelveDataForex(normalizedSymbol) {
		assetType = models.Forex
	}

	// Convert timestamp from seconds to microseconds
	var timestampMicros int64
	if msg.Timestamp > 0 {
		timestampMicros = msg.Timestamp * 1_000_000
	} else {
		timestampMicros = time.Now().UnixMicro()
	}

	tick := &models.Tick{
		Symbol:    normalizedSymbol,
		Price:     msg.Price,
		Bid:       msg.Bid,
		Ask:       msg.Ask,
		Volume:    float64(msg.DayVolume),
		Timestamp: timestampMicros,
		Source:    models.SourceTwelve,
		Type:      assetType,
	}

	log.Printf("[TwelveData] %s | p=%.5f b=%.5f a=%.5f ty=%s", normalizedSymbol, msg.Price, msg.Bid, msg.Ask, assetType)

	// Non-blocking send to avoid backpressure
	select {
	case t.tickChan <- tick:
	default:
		log.Printf("[TwelveData] Tick channel full, dropping tick for %s", normalizedSymbol)
	}
}

// pingLoop sends periodic heartbeat messages to keep the connection alive
// Per Twelve Data docs: Send {"action": "heartbeat"} every 10 seconds
func (t *TwelveAdapter) pingLoop() {
	ticker := time.NewTicker(twelvePingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-t.stopChan:
			return
		case <-ticker.C:
			t.mu.RLock()
			conn := t.conn
			connected := t.connected
			t.mu.RUnlock()

			if connected && conn != nil {
				// Send heartbeat action as per Twelve Data documentation
				heartbeat := TwelveHeartbeat{Action: "heartbeat"}
				if err := conn.WriteJSON(heartbeat); err != nil {
					log.Printf("[TwelveData] Heartbeat failed: %v", err)
				}
			}
		}
	}
}

// handleReconnect attempts to reconnect with exponential backoff
func (t *TwelveAdapter) handleReconnect() {
	t.mu.Lock()
	if t.reconnecting {
		t.mu.Unlock()
		return
	}
	t.reconnecting = true
	if t.conn != nil {
		t.conn.Close()
		t.conn = nil
	}
	t.connected = false
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		t.reconnecting = false
		t.mu.Unlock()
	}()

	baseDelay := time.Duration(t.cfg.Settings.ReconnectDelayMS) * time.Millisecond
	maxAttempts := t.cfg.Settings.MaxReconnectAttempts

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		select {
		case <-t.stopChan:
			return
		default:
		}

		delay := baseDelay * time.Duration(attempt)
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}

		log.Printf("[TwelveData] Reconnection attempt %d/%d in %v", attempt, maxAttempts, delay)
		time.Sleep(delay)

		if err := t.connect(); err != nil {
			log.Printf("[TwelveData] Reconnection failed: %v", err)
			continue
		}

		log.Println("[TwelveData] Reconnected successfully")
		return
	}

	log.Printf("[TwelveData] Max reconnection attempts (%d) reached", maxAttempts)
}
