package engine

import (
	"log"
	"sync"
	"sync/atomic"

	"feed-core/internal/models"
)

// Client represents a connected client (WebSocket or TCP)
type Client interface {
	ID() string
	Send(tick *models.Tick) error
	Close() error
}

// Hub manages all connected clients and broadcasts ticks to them
type Hub struct {
	inputChan   <-chan *models.Tick     // Receives ticks from arbitrator
	clients     map[string]Client       // Registered clients
	mu          sync.RWMutex            // Protects clients map
	register    chan Client             // Channel to register new clients
	unregister  chan Client             // Channel to unregister clients
	stopChan    chan struct{}
	clientCount int64                   // Atomic counter
	tickCount   int64                   // Atomic counter for total ticks
}

// NewHub creates a new Hub instance
func NewHub(inputChan <-chan *models.Tick) *Hub {
	return &Hub{
		inputChan:  inputChan,
		clients:    make(map[string]Client),
		register:   make(chan Client, 100),
		unregister: make(chan Client, 100),
		stopChan:   make(chan struct{}),
	}
}

// Start begins the hub's processing loops
func (h *Hub) Start() {
	go h.registrationLoop()
	go h.broadcastLoop()
	log.Println("[Hub] Started")
}

// Stop gracefully shuts down the hub
func (h *Hub) Stop() {
	close(h.stopChan)

	// Close all clients
	h.mu.Lock()
	for _, client := range h.clients {
		client.Close()
	}
	h.clients = make(map[string]Client)
	h.mu.Unlock()

	log.Println("[Hub] Stopped")
}

// Register adds a new client to the hub
func (h *Hub) Register(client Client) {
	select {
	case h.register <- client:
	default:
		log.Printf("[Hub] Register channel full, client %s may be delayed", client.ID())
		h.register <- client // Blocking send as fallback
	}
}

// Unregister removes a client from the hub
func (h *Hub) Unregister(client Client) {
	select {
	case h.unregister <- client:
	default:
		log.Printf("[Hub] Unregister channel full, client %s may be delayed", client.ID())
		h.unregister <- client // Blocking send as fallback
	}
}

// ClientCount returns the current number of connected clients
func (h *Hub) ClientCount() int64 {
	return atomic.LoadInt64(&h.clientCount)
}

// TotalTicks returns the total number of ticks broadcast
func (h *Hub) TotalTicks() int64 {
	return atomic.LoadInt64(&h.tickCount)
}

// registrationLoop handles client registration and unregistration
func (h *Hub) registrationLoop() {
	for {
		select {
		case <-h.stopChan:
			return

		case client := <-h.register:
			h.mu.Lock()
			if _, exists := h.clients[client.ID()]; !exists {
				h.clients[client.ID()] = client
				atomic.AddInt64(&h.clientCount, 1)
				log.Printf("[Hub] Client registered: %s (total: %d)", client.ID(), h.ClientCount())
			}
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, exists := h.clients[client.ID()]; exists {
				delete(h.clients, client.ID())
				client.Close()
				atomic.AddInt64(&h.clientCount, -1)
				log.Printf("[Hub] Client unregistered: %s (total: %d)", client.ID(), h.ClientCount())
			}
			h.mu.Unlock()
		}
	}
}

// broadcastLoop receives ticks and broadcasts them to all clients
func (h *Hub) broadcastLoop() {
	for {
		select {
		case <-h.stopChan:
			return

		case tick, ok := <-h.inputChan:
			if !ok {
				return
			}
			h.broadcast(tick)
		}
	}
}

// broadcast sends a tick to all connected clients
func (h *Hub) broadcast(tick *models.Tick) {
	atomic.AddInt64(&h.tickCount, 1)

	h.mu.RLock()
	clients := make([]Client, 0, len(h.clients))
	for _, client := range h.clients {
		clients = append(clients, client)
	}
	h.mu.RUnlock()

	// Fan-out to all clients
	var wg sync.WaitGroup
	for _, client := range clients {
		wg.Add(1)
		go func(c Client) {
			defer wg.Done()
			if err := c.Send(tick); err != nil {
				// Client send failed, schedule for removal
				h.Unregister(c)
			}
		}(client)
	}
	// Don't wait for all sends to complete to avoid blocking
	// Failed sends will trigger unregistration
}

// GetClients returns a snapshot of all client IDs
func (h *Hub) GetClients() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	ids := make([]string, 0, len(h.clients))
	for id := range h.clients {
		ids = append(ids, id)
	}
	return ids
}

// BroadcastToSymbol sends a tick only to clients subscribed to a specific symbol
// This is a placeholder for future symbol-based filtering
func (h *Hub) BroadcastToSymbol(tick *models.Tick, subscribers map[string]bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for id, client := range h.clients {
		if subscribers[id] {
			go func(c Client) {
				if err := c.Send(tick); err != nil {
					h.Unregister(c)
				}
			}(client)
		}
	}
}
