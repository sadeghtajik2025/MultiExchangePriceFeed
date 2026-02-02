package ingestion

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"feed-core/internal/config"
	"feed-core/internal/models"

	"github.com/gorilla/websocket"
)

const (
	// Massive/Polygon WebSocket endpoints
	massiveCryptoURL = "wss://socket.polygon.io/crypto"
	massiveForexURL  = "wss://socket.polygon.io/forex"

	massiveReadTimeout  = 30 * time.Second
	massivePingInterval = 10 * time.Second
)

// MassiveCryptoTrade represents crypto trade events (XT)
type MassiveCryptoTrade struct {
	Ev   string  `json:"ev"`   // "XT"
	Pair string  `json:"pair"` // e.g., "BTC-USD"
	P    float64 `json:"p"`    // Price
	S    float64 `json:"s"`    // Size
	T    int64   `json:"t"`    // Timestamp (ms)
	X    int     `json:"x"`    // Exchange ID
}

// MassiveCryptoQuote represents crypto quote events (XQ)
type MassiveCryptoQuote struct {
	Ev   string  `json:"ev"`   // "XQ"
	Pair string  `json:"pair"` // e.g., "BTC-USD"
	Bp   float64 `json:"bp"`   // Bid price
	Bs   float64 `json:"bs"`   // Bid size
	Ap   float64 `json:"ap"`   // Ask price
	As   float64 `json:"as"`   // Ask size
	T    int64   `json:"t"`    // Timestamp (ms)
	X    int     `json:"x"`    // Exchange ID
}

// MassiveCryptoAgg represents crypto aggregate events (XA, XAS)
type MassiveCryptoAgg struct {
	Ev   string  `json:"ev"`   // "XA" or "XAS"
	Pair string  `json:"pair"` // e.g., "BTC-USD"
	O    float64 `json:"o"`    // Open
	C    float64 `json:"c"`    // Close
	H    float64 `json:"h"`    // High
	L    float64 `json:"l"`    // Low
	V    float64 `json:"v"`    // Volume
	S    int64   `json:"s"`    // Start timestamp (ms)
	E    int64   `json:"e"`    // End timestamp (ms)
	Vw   float64 `json:"vw"`   // Volume weighted average price
}

// MassiveForexQuote represents forex quote events (C)
type MassiveForexQuote struct {
	Ev string  `json:"ev"` // "C"
	P  string  `json:"p"`  // Pair e.g., "EUR/USD"
	A  float64 `json:"a"`  // Ask
	B  float64 `json:"b"`  // Bid
	T  int64   `json:"t"`  // Timestamp (ms)
	X  int     `json:"x"`  // Exchange ID
}

// MassiveForexAgg represents forex aggregate events (CA, CAS)
type MassiveForexAgg struct {
	Ev   string  `json:"ev"`   // "CA" or "CAS"
	Pair string  `json:"pair"` // e.g., "EUR/USD"
	O    float64 `json:"o"`    // Open
	C    float64 `json:"c"`    // Close
	H    float64 `json:"h"`    // High
	L    float64 `json:"l"`    // Low
	V    int     `json:"v"`    // Volume
	S    int64   `json:"s"`    // Start timestamp (ms)
}

// MassiveStatusMessage for status events
type MassiveStatusMessage struct {
	Ev     string `json:"ev"`
	Status string `json:"status"`
	Msg    string `json:"message"`
}

// MassiveAuthMessage is sent to authenticate
type MassiveAuthMessage struct {
	Action string `json:"action"` // "auth"
	Params string `json:"params"` // API key
}

// MassiveSubscribeMessage is sent to subscribe to symbols
type MassiveSubscribeMessage struct {
	Action string `json:"action"` // "subscribe"
	Params string `json:"params"` // Comma-separated symbols with prefix
}

// MassiveConnection represents a single WebSocket connection to Massive
type MassiveConnection struct {
	name         string
	url          string
	apiKey       string
	symbols      []string // Provider format (e.g., "XT.BTC-USD")
	assetType    models.AssetType
	conn         *websocket.Conn
	tickChan     chan<- *models.Tick
	stopChan     chan struct{}
	reconnecting bool
	mu           sync.RWMutex
	connected    bool
	cfg          *config.Config
}

// MassiveAdapter manages multiple connections to Massive/Polygon
type MassiveAdapter struct {
	cfg        *config.Config
	tickChan   chan<- *models.Tick
	cryptoConn *MassiveConnection
	forexConn  *MassiveConnection
	stopChan   chan struct{}
}

// NewMassiveAdapter creates a new Massive/Polygon adapter with separate crypto and forex connections
func NewMassiveAdapter(cfg *config.Config, tickChan chan<- *models.Tick) *MassiveAdapter {
	stopChan := make(chan struct{})

	adapter := &MassiveAdapter{
		cfg:      cfg,
		tickChan: tickChan,
		stopChan: stopChan,
	}

	// Create crypto connection if we have crypto symbols configured for Massive
	if cfg.HasMassiveCryptoSymbols() {
		adapter.cryptoConn = &MassiveConnection{
			name:      "Crypto",
			url:       massiveCryptoURL,
			apiKey:    cfg.Credentials.Massive,
			symbols:   cfg.GetMassiveCryptoSymbols(),
			assetType: models.Crypto,
			tickChan:  tickChan,
			stopChan:  stopChan,
			cfg:       cfg,
		}
	}

	// Create forex connection if we have forex symbols configured for Massive
	if cfg.HasMassiveForexSymbols() {
		adapter.forexConn = &MassiveConnection{
			name:      "Forex",
			url:       massiveForexURL,
			apiKey:    cfg.Credentials.Massive,
			symbols:   cfg.GetMassiveForexSymbols(),
			assetType: models.Forex,
			tickChan:  tickChan,
			stopChan:  stopChan,
			cfg:       cfg,
		}
	}

	return adapter
}

// Start begins both crypto and forex connections
func (m *MassiveAdapter) Start() error {
	if m.cryptoConn != nil {
		if err := m.cryptoConn.Start(); err != nil {
			log.Printf("[Massive] Crypto connection failed: %v", err)
		}
	}

	if m.forexConn != nil {
		if err := m.forexConn.Start(); err != nil {
			log.Printf("[Massive] Forex connection failed: %v", err)
		}
	}

	return nil
}

// Stop gracefully shuts down all connections
func (m *MassiveAdapter) Stop() {
	close(m.stopChan)
	if m.cryptoConn != nil {
		m.cryptoConn.Stop()
	}
	if m.forexConn != nil {
		m.forexConn.Stop()
	}
}

// IsConnected returns true if at least one connection is active
func (m *MassiveAdapter) IsConnected() bool {
	cryptoConnected := m.cryptoConn != nil && m.cryptoConn.IsConnected()
	forexConnected := m.forexConn != nil && m.forexConn.IsConnected()
	return cryptoConnected || forexConnected
}

// Start begins a single Massive connection
func (c *MassiveConnection) Start() error {
	if err := c.connect(); err != nil {
		return err
	}

	go c.readLoop()
	go c.pingLoop()

	return nil
}

// Stop gracefully shuts down the connection
func (c *MassiveConnection) Stop() {
	c.mu.Lock()
	if c.conn != nil {
		c.conn.Close()
	}
	c.connected = false
	c.mu.Unlock()
}

// IsConnected returns connection status
func (c *MassiveConnection) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// connect establishes WebSocket connection and authenticates
func (c *MassiveConnection) connect() error {
	conn, _, err := websocket.DefaultDialer.Dial(c.url, nil)
	if err != nil {
		return fmt.Errorf("massive %s connection failed: %w", c.name, err)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	log.Printf("[Massive] %s connected to WebSocket", c.name)

	// Authenticate
	if err := c.authenticate(); err != nil {
		c.conn.Close()
		return fmt.Errorf("authentication failed: %w", err)
	}

	// Subscribe to symbols
	if err := c.subscribe(); err != nil {
		c.conn.Close()
		return fmt.Errorf("subscription failed: %w", err)
	}

	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()

	return nil
}

// authenticate sends authentication message
func (c *MassiveConnection) authenticate() error {
	auth := MassiveAuthMessage{
		Action: "auth",
		Params: c.apiKey,
	}

	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if err := conn.WriteJSON(auth); err != nil {
		return fmt.Errorf("auth write failed: %w", err)
	}

	// Read authentication response
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("auth response failed: %w", err)
	}

	var response []MassiveStatusMessage
	if err := json.Unmarshal(msg, &response); err != nil {
		// Try single message format
		var single MassiveStatusMessage
		if err := json.Unmarshal(msg, &single); err != nil {
			return fmt.Errorf("auth response parse failed: %w", err)
		}
		if single.Status != "auth_success" && single.Status != "connected" {
			return fmt.Errorf("auth failed: %s", single.Msg)
		}
	} else {
		for _, r := range response {
			if r.Status == "auth_failed" {
				return fmt.Errorf("auth failed: %s", r.Msg)
			}
		}
	}

	log.Printf("[Massive] %s authenticated successfully", c.name)
	return nil
}

// subscribe sends subscription request for all symbols
func (c *MassiveConnection) subscribe() error {
	// Build comma-separated symbol list
	params := ""
	for i, sym := range c.symbols {
		if i > 0 {
			params += ","
		}
		params += sym
	}

	sub := MassiveSubscribeMessage{
		Action: "subscribe",
		Params: params,
	}

	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if err := conn.WriteJSON(sub); err != nil {
		return fmt.Errorf("subscribe write failed: %w", err)
	}

	log.Printf("[Massive] %s subscribed to %d symbols: %s", c.name, len(c.symbols), params)
	return nil
}

// readLoop continuously reads messages from the WebSocket
func (c *MassiveConnection) readLoop() {
	defer func() {
		c.mu.Lock()
		c.connected = false
		c.mu.Unlock()
	}()

	for {
		select {
		case <-c.stopChan:
			return
		default:
		}

		c.mu.RLock()
		conn := c.conn
		c.mu.RUnlock()

		if conn == nil {
			c.handleReconnect()
			continue
		}

		conn.SetReadDeadline(time.Now().Add(massiveReadTimeout))

		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("[Massive] %s read error: %v", c.name, err)
			c.handleReconnect()
			continue
		}

		c.processMessage(message)
	}
}

// processMessage parses and handles incoming WebSocket messages
func (c *MassiveConnection) processMessage(data []byte) {
	// Polygon.io sends messages as arrays: [{"ev":"XT",...},{"ev":"XT",...}]
	// First, determine if data is an array or single object
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return
	}

	// Check if it starts with '[' (array) or '{' (object)
	if data[0] == '[' {
		// It's an array - parse each element
		var rawMessages []json.RawMessage
		if err := json.Unmarshal(data, &rawMessages); err != nil {
			log.Printf("[Massive] %s array parse error: %v", c.name, err)
			return
		}
		for _, raw := range rawMessages {
			c.processSingleMessage(raw)
		}
	} else if data[0] == '{' {
		// It's a single object
		c.processSingleMessage(data)
	} else {
		log.Printf("[Massive] %s unexpected message format: %s", c.name, string(data[:min(50, len(data))]))
	}
}

// processSingleMessage handles a single JSON message object
func (c *MassiveConnection) processSingleMessage(raw []byte) {
	// First determine event type
	var baseMsg struct {
		Ev     string `json:"ev"`
		Status string `json:"status"`
		Msg    string `json:"message"`
	}
	if err := json.Unmarshal(raw, &baseMsg); err != nil {
		// Could be nested array, try to parse as array
		if raw[0] == '[' {
			var nested []json.RawMessage
			if err := json.Unmarshal(raw, &nested); err == nil {
				for _, n := range nested {
					c.processSingleMessage(n)
				}
				return
			}
		}
		log.Printf("[Massive] %s message parse error: %v", c.name, err)
		return
	}

	switch baseMsg.Ev {
	case "XT": // Crypto trade
		var msg MassiveCryptoTrade
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("[Massive] %s crypto trade parse error: %v", c.name, err)
			return
		}
		c.handleCryptoTrade(msg)

	case "XQ": // Crypto quote
		var msg MassiveCryptoQuote
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("[Massive] %s crypto quote parse error: %v", c.name, err)
			return
		}
		c.handleCryptoQuote(msg)

	case "XA", "XAS": // Crypto aggregates
		var msg MassiveCryptoAgg
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("[Massive] %s crypto agg parse error: %v", c.name, err)
			return
		}
		c.handleCryptoAgg(msg)

	case "C": // Forex quote
		var msg MassiveForexQuote
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("[Massive] %s forex quote parse error: %v", c.name, err)
			return
		}
		c.handleForexQuote(msg)

	case "CA", "CAS": // Forex aggregates
		var msg MassiveForexAgg
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("[Massive] %s forex agg parse error: %v", c.name, err)
			return
		}
		c.handleForexAgg(msg)

	case "status":
		if baseMsg.Status == "connected" || baseMsg.Status == "auth_success" {
			log.Printf("[Massive] %s status: %s", c.name, baseMsg.Status)
		} else if baseMsg.Status == "error" || baseMsg.Status == "auth_failed" {
			log.Printf("[Massive] %s error: %s", c.name, baseMsg.Msg)
		}

	default:
		// Log all unhandled events for debugging
		if baseMsg.Ev != "" {
			log.Printf("[Massive] %s unhandled event type: %s", c.name, baseMsg.Ev)
		} else if baseMsg.Status != "" {
			log.Printf("[Massive] %s status event: %s - %s", c.name, baseMsg.Status, baseMsg.Msg)
		}
	}
}

// handleCryptoTrade handles crypto trade events (XT)
func (c *MassiveConnection) handleCryptoTrade(msg MassiveCryptoTrade) {
	if msg.Pair == "" || msg.P == 0 {
		return
	}

	tick := &models.Tick{
		Symbol:    msg.Pair,
		Price:     msg.P,
		Volume:    msg.S,
		Timestamp: msg.T * 1000, // ms to micros
		Source:    models.SourceMassive,
		Type:      models.Crypto,
	}

	log.Printf("[Massive] XT %s | p=%.2f v=%.6f ty=CRYPTO", msg.Pair, msg.P, msg.S)
	c.sendTick(tick)
}

// handleCryptoQuote handles crypto quote events (XQ)
func (c *MassiveConnection) handleCryptoQuote(msg MassiveCryptoQuote) {
	if msg.Pair == "" {
		return
	}

	// Use mid price as the price
	price := (msg.Bp + msg.Ap) / 2
	if price == 0 {
		return
	}

	tick := &models.Tick{
		Symbol:    msg.Pair,
		Price:     price,
		Bid:       msg.Bp,
		Ask:       msg.Ap,
		Timestamp: msg.T * 1000,
		Source:    models.SourceMassive,
		Type:      models.Crypto,
	}

	log.Printf("[Massive] XQ %s | p=%.2f b=%.2f a=%.2f ty=CRYPTO", msg.Pair, price, msg.Bp, msg.Ap)
	c.sendTick(tick)
}

// handleCryptoAgg handles crypto aggregate events (XA, XAS)
func (c *MassiveConnection) handleCryptoAgg(msg MassiveCryptoAgg) {
	if msg.Pair == "" || msg.C == 0 {
		return
	}

	tick := &models.Tick{
		Symbol:    msg.Pair,
		Open:      msg.O,
		High:      msg.H,
		Low:       msg.L,
		Close:     msg.C,
		Volume:    msg.V,
		Timestamp: msg.S * 1000, // Start timestamp
		Source:    models.SourceMassive,
		Type:      models.Crypto,
	}

	log.Printf("[Massive] %s %s | O=%.2f H=%.2f L=%.2f C=%.2f v=%.2f ty=CRYPTO", msg.Ev, msg.Pair, msg.O, msg.H, msg.L, msg.C, msg.V)
	c.sendTick(tick)
}

// handleForexQuote handles forex quote events (C)
func (c *MassiveConnection) handleForexQuote(msg MassiveForexQuote) {
	if msg.P == "" {
		return
	}

	// Normalize symbol from "EUR/USD" to "EUR-USD"
	normalizedSymbol := config.NormalizeFromTwelve(msg.P)

	// Use mid price as the price
	price := (msg.A + msg.B) / 2
	if price == 0 {
		return
	}

	tick := &models.Tick{
		Symbol:    normalizedSymbol,
		Price:     price,
		Bid:       msg.B,
		Ask:       msg.A,
		Timestamp: msg.T * 1000,
		Source:    models.SourceMassive,
		Type:      models.Forex,
	}

	log.Printf("[Massive] C %s | p=%.5f b=%.5f a=%.5f ty=FOREX", normalizedSymbol, price, msg.B, msg.A)
	c.sendTick(tick)
}

// handleForexAgg handles forex aggregate events (CA, CAS)
func (c *MassiveConnection) handleForexAgg(msg MassiveForexAgg) {
	if msg.Pair == "" || msg.C == 0 {
		return
	}

	// Normalize symbol
	normalizedSymbol := config.NormalizeFromTwelve(msg.Pair)

	tick := &models.Tick{
		Symbol:    normalizedSymbol,
		Open:      msg.O,
		High:      msg.H,
		Low:       msg.L,
		Close:     msg.C,
		Volume:    float64(msg.V),
		Timestamp: msg.S * 1000,
		Source:    models.SourceMassive,
		Type:      models.Forex,
	}

	log.Printf("[Massive] %s %s | O=%.5f H=%.5f L=%.5f C=%.5f ty=FOREX", msg.Ev, normalizedSymbol, msg.O, msg.H, msg.L, msg.C)
	c.sendTick(tick)
}

// sendTick sends a tick to the channel (non-blocking)
func (c *MassiveConnection) sendTick(tick *models.Tick) {
	select {
	case c.tickChan <- tick:
	default:
		log.Printf("[Massive] %s tick channel full, dropping tick for %s", c.name, tick.Symbol)
	}
}

// pingLoop sends periodic pings to keep the connection alive
func (c *MassiveConnection) pingLoop() {
	ticker := time.NewTicker(massivePingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopChan:
			return
		case <-ticker.C:
			c.mu.RLock()
			conn := c.conn
			connected := c.connected
			c.mu.RUnlock()

			if connected && conn != nil {
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					log.Printf("[Massive] %s ping failed: %v", c.name, err)
				}
			}
		}
	}
}

// handleReconnect attempts to reconnect with exponential backoff
func (c *MassiveConnection) handleReconnect() {
	c.mu.Lock()
	if c.reconnecting {
		c.mu.Unlock()
		return
	}
	c.reconnecting = true
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.connected = false
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.reconnecting = false
		c.mu.Unlock()
	}()

	baseDelay := time.Duration(c.cfg.Settings.ReconnectDelayMS) * time.Millisecond
	maxAttempts := c.cfg.Settings.MaxReconnectAttempts

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		select {
		case <-c.stopChan:
			return
		default:
		}

		delay := baseDelay * time.Duration(attempt)
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}

		log.Printf("[Massive] %s reconnection attempt %d/%d in %v", c.name, attempt, maxAttempts, delay)
		time.Sleep(delay)

		if err := c.connect(); err != nil {
			log.Printf("[Massive] %s reconnection failed: %v", c.name, err)
			continue
		}

		log.Printf("[Massive] %s reconnected successfully", c.name)
		return
	}

	log.Printf("[Massive] %s max reconnection attempts (%d) reached", c.name, maxAttempts)
}
