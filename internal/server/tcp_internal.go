package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"feed-core/internal/engine"
	"feed-core/internal/models"
)

const (
	tcpWriteTimeout   = 5 * time.Second
	tcpReadTimeout    = 60 * time.Second
	tcpBufferSize     = 4096
	tcpSendBufferSize = 1024
)

// TCPClient represents a connected Trading Engine client
type TCPClient struct {
	id       string
	conn     net.Conn
	send     chan *models.Tick
	hub      *engine.Hub
	closed   int32
	mu       sync.Mutex
}

// NewTCPClient creates a new TCP client
func NewTCPClient(id string, conn net.Conn, hub *engine.Hub) *TCPClient {
	return &TCPClient{
		id:   id,
		conn: conn,
		send: make(chan *models.Tick, tcpSendBufferSize),
		hub:  hub,
	}
}

// ID returns the client's unique identifier
func (c *TCPClient) ID() string {
	return c.id
}

// Send queues a tick for sending to the client
func (c *TCPClient) Send(tick *models.Tick) error {
	if atomic.LoadInt32(&c.closed) == 1 {
		return fmt.Errorf("client closed")
	}

	select {
	case c.send <- tick:
		return nil
	default:
		return fmt.Errorf("send buffer full")
	}
}

// Close gracefully closes the client connection
func (c *TCPClient) Close() error {
	if !atomic.CompareAndSwapInt32(&c.closed, 0, 1) {
		return nil // Already closed
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	close(c.send)
	return c.conn.Close()
}

// readLoop reads commands from the TCP connection
func (c *TCPClient) readLoop() {
	defer func() {
		c.hub.Unregister(c)
		c.conn.Close()
	}()

	reader := bufio.NewReader(c.conn)

	for {
		if atomic.LoadInt32(&c.closed) == 1 {
			return
		}

		c.conn.SetReadDeadline(time.Now().Add(tcpReadTimeout))

		line, err := reader.ReadString('\n')
		if err != nil {
			if atomic.LoadInt32(&c.closed) == 0 {
				log.Printf("[TCP] Client %s read error: %v", c.id, err)
			}
			return
		}

		// Handle command (for future use: subscription management)
		c.handleCommand(line)
	}
}

// handleCommand processes incoming client commands
func (c *TCPClient) handleCommand(cmd string) {
	// For now, just log commands
	// Future: handle SUBSCRIBE, UNSUBSCRIBE, PING, etc.
	log.Printf("[TCP] Client %s command: %s", c.id, cmd)
}

// writeLoop sends ticks to the TCP connection
func (c *TCPClient) writeLoop() {
	defer c.conn.Close()

	for tick := range c.send {
		if atomic.LoadInt32(&c.closed) == 1 {
			return
		}

		c.conn.SetWriteDeadline(time.Now().Add(tcpWriteTimeout))

		// Serialize as compact JSON with newline delimiter
		data, err := json.Marshal(tick)
		if err != nil {
			log.Printf("[TCP] Client %s marshal error: %v", c.id, err)
			continue
		}

		// Append newline for message framing
		data = append(data, '\n')

		if _, err := c.conn.Write(data); err != nil {
			log.Printf("[TCP] Client %s write error: %v", c.id, err)
			return
		}
	}
}

// TCPServer manages the dedicated TCP endpoint for Trading Engine
type TCPServer struct {
	hub       *engine.Hub
	port      int
	listener  net.Listener
	stopChan  chan struct{}
	clientSeq int64
	wg        sync.WaitGroup
}

// NewTCPServer creates a new TCP server for internal use
func NewTCPServer(hub *engine.Hub, port int) *TCPServer {
	return &TCPServer{
		hub:      hub,
		port:     port,
		stopChan: make(chan struct{}),
	}
}

// Start begins listening for TCP connections
func (s *TCPServer) Start() error {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return fmt.Errorf("failed to start TCP server: %w", err)
	}

	s.listener = listener
	log.Printf("[TCP] Server starting on port %d (Trading Engine dedicated)", s.port)

	s.wg.Add(1)
	go s.acceptLoop()

	return nil
}

// Stop gracefully shuts down the TCP server
func (s *TCPServer) Stop() error {
	close(s.stopChan)

	if s.listener != nil {
		s.listener.Close()
	}

	s.wg.Wait()
	log.Println("[TCP] Server stopped")
	return nil
}

// acceptLoop accepts new TCP connections
func (s *TCPServer) acceptLoop() {
	defer s.wg.Done()

	for {
		select {
		case <-s.stopChan:
			return
		default:
		}

		// Set accept deadline to allow checking stopChan
		if tcpListener, ok := s.listener.(*net.TCPListener); ok {
			tcpListener.SetDeadline(time.Now().Add(1 * time.Second))
		}

		conn, err := s.listener.Accept()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue // Timeout is expected, check stopChan
			}

			select {
			case <-s.stopChan:
				return
			default:
				log.Printf("[TCP] Accept error: %v", err)
				continue
			}
		}

		// Handle new connection
		go s.handleConnection(conn)
	}
}

// handleConnection sets up a new TCP client
func (s *TCPServer) handleConnection(conn net.Conn) {
	clientID := fmt.Sprintf("tcp-%d-%d", time.Now().UnixNano(), atomic.AddInt64(&s.clientSeq, 1))

	// Configure socket options for low latency
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(true)      // Disable Nagle's algorithm
		tcpConn.SetKeepAlive(true)    // Enable keep-alive
		tcpConn.SetKeepAlivePeriod(30 * time.Second)
	}

	client := NewTCPClient(clientID, conn, s.hub)
	s.hub.Register(client)

	log.Printf("[TCP] Trading Engine client connected: %s from %s", clientID, conn.RemoteAddr())

	// Send welcome message
	welcome := fmt.Sprintf(`{"type":"connected","client_id":"%s"}` + "\n", clientID)
	conn.Write([]byte(welcome))

	// Start read and write loops
	go client.writeLoop()
	go client.readLoop()
}
