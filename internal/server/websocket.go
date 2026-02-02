package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"feed-core/internal/engine"
	"feed-core/internal/models"

	"github.com/gorilla/websocket"
)

const (
	// WebSocket configuration
	writeWait      = 10 * time.Second    // Time allowed to write a message
	pongWait       = 60 * time.Second    // Time allowed to read pong
	pingPeriod     = (pongWait * 9) / 10 // Ping period (must be < pongWait)
	maxMessageSize = 512                 // Max message size from client
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins (configure in production)
	},
}

// WebSocketClient represents a single WebSocket connection
type WebSocketClient struct {
	id       string
	conn     *websocket.Conn
	send     chan *models.Tick
	hub      *engine.Hub
	closed   int32
	mu       sync.Mutex
}

// NewWebSocketClient creates a new WebSocket client
func NewWebSocketClient(id string, conn *websocket.Conn, hub *engine.Hub) *WebSocketClient {
	return &WebSocketClient{
		id:   id,
		conn: conn,
		send: make(chan *models.Tick, 256), // Buffered channel
		hub:  hub,
	}
}

// ID returns the client's unique identifier
func (c *WebSocketClient) ID() string {
	return c.id
}

// Send queues a tick for sending to the client
func (c *WebSocketClient) Send(tick *models.Tick) error {
	if atomic.LoadInt32(&c.closed) == 1 {
		return fmt.Errorf("client closed")
	}

	select {
	case c.send <- tick:
		return nil
	default:
		// Channel full, client is too slow
		return fmt.Errorf("send buffer full")
	}
}

// Close gracefully closes the client connection
func (c *WebSocketClient) Close() error {
	if !atomic.CompareAndSwapInt32(&c.closed, 0, 1) {
		return nil // Already closed
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	close(c.send)
	return c.conn.Close()
}

// readPump reads messages from the WebSocket connection
func (c *WebSocketClient) readPump() {
	defer func() {
		c.hub.Unregister(c)
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[WebSocket] Client %s read error: %v", c.id, err)
			}
			break
		}

		// Handle client messages (e.g., subscription requests)
		c.handleMessage(message)
	}
}

// handleMessage processes incoming client messages
func (c *WebSocketClient) handleMessage(message []byte) {
	// For now, we just log client messages
	// Future: handle subscription/unsubscription requests
	log.Printf("[WebSocket] Client %s message: %s", c.id, string(message))
}

// writePump sends messages to the WebSocket connection
func (c *WebSocketClient) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case tick, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Channel closed
				c.conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}

			// Serialize tick to JSON
			data, err := json.Marshal(tick)
			if err != nil {
				log.Printf("[WebSocket] Client %s marshal error: %v", c.id, err)
				continue
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				log.Printf("[WebSocket] Client %s write error: %v", c.id, err)
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// WebSocketServer manages the WebSocket endpoint
type WebSocketServer struct {
	hub       *engine.Hub
	port      int
	server    *http.Server
	clientSeq int64 // For generating unique client IDs
}

// NewWebSocketServer creates a new WebSocket server
func NewWebSocketServer(hub *engine.Hub, port int) *WebSocketServer {
	return &WebSocketServer{
		hub:  hub,
		port: port,
	}
}

// Start begins listening for WebSocket connections
func (s *WebSocketServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/stats", s.handleStats)

	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: mux,
	}

	log.Printf("[WebSocket] Server starting on port %d", s.port)

	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[WebSocket] Server error: %v", err)
		}
	}()

	return nil
}

// Stop gracefully shuts down the WebSocket server
func (s *WebSocketServer) Stop() error {
	if s.server != nil {
		return s.server.Close()
	}
	return nil
}

// handleWebSocket upgrades HTTP connections to WebSocket
func (s *WebSocketServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WebSocket] Upgrade error: %v", err)
		return
	}

	// Generate unique client ID
	clientID := fmt.Sprintf("ws-%d-%d", time.Now().UnixNano(), atomic.AddInt64(&s.clientSeq, 1))

	client := NewWebSocketClient(clientID, conn, s.hub)
	s.hub.Register(client)

	log.Printf("[WebSocket] New client connected: %s from %s", clientID, r.RemoteAddr)

	// Start read and write pumps
	go client.writePump()
	go client.readPump()
}

// handleHealth returns a simple health check response
func (s *WebSocketServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "healthy",
		"clients": s.hub.ClientCount(),
		"ticks":   s.hub.TotalTicks(),
	})
}

// handleStats returns detailed statistics
func (s *WebSocketServer) handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"connected_clients": s.hub.ClientCount(),
		"total_ticks":       s.hub.TotalTicks(),
		"client_ids":        s.hub.GetClients(),
	})
}
