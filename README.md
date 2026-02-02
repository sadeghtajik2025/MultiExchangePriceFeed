# Feed Core - Real-Time Financial Data Feed Platform

A high-performance, production-ready real-time market data feed platform built in Go. Features automatic failover between configurable primary and secondary data sources (Twelve Data and Massive/Polygon) using the "Silence Detector" strategy with per-symbol failover logic.

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              Feed Core                                       │
│                                                                             │
│  ┌──────────────────┐    ┌──────────────────┐    ┌──────────────────┐      │
│  │   Twelve Data    │    │                  │    │   WebSocket      │      │
│  │   Adapter        │───▶│   Arbitrator     │───▶│   Server (:8080) │──────│───▶ Web Clients
│  └──────────────────┘    │   (Per-Symbol    │    │                  │      │
│                          │    Silence       │    └──────────────────┘      │
│  ┌──────────────────┐    │    Detector)     │                               │
│  │   Massive/Polygon│───▶│                  │    ┌──────────────────┐      │
│  │   Adapter        │    │                  │───▶│   TCP Server     │──────│───▶ Trading Engine
│  └──────────────────┘    └──────────────────┘    │   (:9000)        │      │
│                                                   └──────────────────┘      │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Features

- **Hub-and-Spoke Architecture**: Centralized data processing with efficient fan-out
- **Configurable Primary/Backup**: Choose which data source is primary via config
- **Per-Symbol Failover**: Independent silence detection for each symbol
- **Separate Symbol Lists**: Configure different symbols for each data source
- **Massive Event Types**: Subscribe to trades, quotes, and aggregates (per-second/per-minute)
- **Wildcard Support**: Use `*` to subscribe to all symbols from Massive/Polygon
- **Dual Output Channels**: WebSocket for web clients, TCP for Trading Engine
- **Normalized Data Format**: Unified `Tick` format regardless of source
- **Production Ready**: Reconnection logic, graceful shutdown, metrics logging
- **Thread-Safe**: Uses channels and atomic operations (no mutex on hot path)

## Quick Start

### Prerequisites

- Go 1.23+
- API keys for Twelve Data and Massive/Polygon

### Installation

```bash
# Clone or navigate to the project
cd feed-core

# Download dependencies
go mod tidy

# Build
go build -o feed-core .

# Run
./feed-core -config config.yaml
```

### Configuration

Edit `config.yaml`:

```yaml
server:
  port: 8080          # WebSocket server port
  tcp_port: 9000      # TCP server port (Trading Engine)

credentials:
  twelve_data: "YOUR_TWELVE_KEY"
  massive: "YOUR_MASSIVE_KEY"

settings:
  primary_provider: "massive"        # Primary data source: "twelve" or "massive"
  primary_silence_threshold_ms: 500  # Per-symbol failover threshold
  reconnect_delay_ms: 1000           # Reconnection delay
  max_reconnect_attempts: 10         # Max reconnection attempts

# Massive event types to subscribe to
massive_events:
  crypto:
    - "XT"   # Trades
    - "XQ"   # Quotes
    - "XA"   # Aggregates per minute
    - "XAS"  # Aggregates per second
  forex:
    - "C"    # Quotes
    - "CA"   # Aggregates per minute
    - "CAS"  # Aggregates per second

# Twelve Data symbols (no wildcard support - must list explicitly)
twelve_data_symbols:
  crypto:
    - "BTC/USD"
    - "ETH/USD"
  forex:
    - "EUR/USD"
    - "GBP/USD"

# Massive/Polygon symbols (supports "*" wildcard for all pairs)
massive_symbols:
  crypto:
    - "*"    # All crypto pairs
  forex:
    - "*"    # All forex pairs
```

### Symbol Configuration

Each data source has its own symbol list with different behaviors:

| Source | Format | Wildcard | Example |
|--------|--------|----------|---------|
| Twelve Data | `BTC/USD` (slash) | No | `"BTC/USD", "EUR/USD"` |
| Massive | `BTC-USD` (dash) | Yes (`*`) | `"*"` or `"BTC-USD"` |

**Failover Behavior:**
- **Symbol in BOTH sources**: Primary is used, failover to backup if primary is silent
- **Symbol in PRIMARY only**: Only primary provides data (no failover possible)
- **Symbol in BACKUP only**: Backup provides data directly (no failover needed)

## API Endpoints

### WebSocket (`ws://localhost:8080/ws`)

Connect to receive real-time price ticks:

```javascript
const ws = new WebSocket('ws://localhost:8080/ws');

ws.onmessage = (event) => {
  const tick = JSON.parse(event.data);
  console.log(`${tick.s}: $${tick.p}`);
};
```

**Quote Tick Format (for trades/quotes):**
```json
{
  "s": "BTC-USD",     // Symbol
  "p": 67234.50,      // Price
  "b": 67234.00,      // Bid
  "a": 67235.00,      // Ask
  "v": 1.5,           // Volume
  "t": 1706363123456789, // Timestamp (microseconds)
  "ty": "CRYPTO"      // Type: "CRYPTO" or "FOREX"
}
```

**OHLC Tick Format (for aggregates):**
```json
{
  "s": "BTC-USD",     // Symbol
  "O": 67200.00,      // Open
  "H": 67300.00,      // High
  "L": 67150.00,      // Low
  "C": 67234.50,      // Close
  "v": 125.5,         // Volume
  "t": 1706363123456789, // Timestamp (microseconds)
  "ty": "CRYPTO"      // Type: "CRYPTO" or "FOREX"
}
```

### TCP (`localhost:9000`)

Low-latency connection for Trading Engine. Feed Core runs a TCP **server** - your Trading Engine connects as a **client**.

```bash
# Test with netcat
nc localhost 9000

# Receives newline-delimited JSON ticks:
{"s":"BTC-USD","p":67234.50,"b":67234.00,"a":67235.00,"v":1.5,"t":1706363123456789,"ty":"CRYPTO"}
```

**Trading Engine Client Example (Go):**

```go
package main

import (
    "bufio"
    "encoding/json"
    "log"
    "net"
)

// Tick for quote data (trades/quotes)
type Tick struct {
    Symbol    string  `json:"s"`
    Price     float64 `json:"p,omitempty"`
    Bid       float64 `json:"b,omitempty"`
    Ask       float64 `json:"a,omitempty"`
    Open      float64 `json:"O,omitempty"`
    High      float64 `json:"H,omitempty"`
    Low       float64 `json:"L,omitempty"`
    Close     float64 `json:"C,omitempty"`
    Volume    float64 `json:"v,omitempty"`
    Timestamp int64   `json:"t"`
    Type      string  `json:"ty"`
}

func main() {
    // Connect to Feed Core (replace with actual IP if remote)
    conn, err := net.Dial("tcp", "localhost:9000")
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()

    reader := bufio.NewReader(conn)
    for {
        line, err := reader.ReadString('\n')
        if err != nil {
            log.Fatal(err)
        }

        var tick Tick
        json.Unmarshal([]byte(line), &tick)
        
        // Use tick in your trading logic
        log.Printf("%s: %.5f (source: %s)", tick.Symbol, tick.Price, tick.Source)
    }
}
```

**Network Setup:**
- Same machine: `localhost:9000`
- Over network: `FEED_CORE_IP:9000` (ensure port 9000 is open in firewall)

### HTTP Endpoints

- `GET /health` - Health check with basic stats
- `GET /stats` - Detailed statistics

## Failover Strategy

The "Silence Detector" strategy uses **per-symbol failover** logic:

1. **Configurable Primary**: Set `primary_provider` to `"twelve"` or `"massive"` in config
2. **Per-Symbol Tracking**: Each symbol's last tick timestamp from primary is tracked independently
3. **Independent Failover**: If BTC-USD is silent from primary but EUR-USD is flowing, only BTC-USD fails over
4. **Failover Activation**: If primary is silent for >500ms (configurable) for a specific symbol, backup ticks are used
5. **Source Tagging**: Failover ticks are tagged (e.g., `src: "twelve_failover"` or `src: "massive_failover"`)
6. **Automatic Recovery**: When primary resumes for a symbol, it immediately takes priority again

**Example:**
| Symbol | Primary (Massive) Last Tick | Status |
|--------|----------------------------|--------|
| BTC-USD | 100ms ago | Using Primary |
| EUR-USD | 600ms ago | Failover to Backup |
| ETH-USD | 200ms ago | Using Primary |

## Project Structure

```
feed-core/
├── go.mod                    # Module definition
├── config.yaml               # Configuration file
├── main.go                   # Entry point
├── README.md                 # This file
└── internal/
    ├── config/
    │   └── config.go         # Configuration loader
    ├── models/
    │   └── tick.go           # Tick data structure
    ├── ingestion/
    │   ├── twelve_adapter.go # Twelve Data WebSocket adapter
    │   └── massive_adapter.go# Massive/Polygon adapter
    ├── engine/
    │   ├── arbitrator.go     # Failover logic (Silence Detector)
    │   └── hub.go            # Fan-out to clients
    └── server/
        ├── websocket.go      # WebSocket server
        └── tcp_internal.go   # TCP server for Trading Engine
```

## Massive Event Types

Massive/Polygon supports multiple event types for different data granularities:

| Event | Type | Description |
|-------|------|-------------|
| `XT` | Crypto | Real-time trades |
| `XQ` | Crypto | Real-time quotes (bid/ask) |
| `XA` | Crypto | Aggregates per minute (OHLCV) |
| `XAS` | Crypto | Aggregates per second (OHLCV) |
| `C` | Forex | Real-time quotes (bid/ask) |
| `CA` | Forex | Aggregates per minute (OHLCV) |
| `CAS` | Forex | Aggregates per second (OHLCV) |

Configure which events to subscribe to in `massive_events` section of config.

## Metrics

The system logs metrics every 30 seconds:

```
[Metrics] Clients: 5 | Ticks: 12345 | Primary: 10000 | Backup: 2500 | Failovers: 12 | Dropped: 2488
```

- **Clients**: Connected WebSocket + TCP clients
- **Ticks**: Total ticks broadcast
- **Primary**: Ticks processed from primary source
- **Backup**: Ticks processed from backup source (during failover or exclusive symbols)
- **Failovers**: Number of per-symbol failover activations
- **Dropped**: Backup ticks dropped (primary was healthy for that symbol)

## Symbol Normalization

The system uses a unified "DASH" format internally (`BTC-USD`):

| Source | Input Format | Normalized |
|--------|-------------|------------|
| Twelve Data | `BTC/USD` | `BTC-USD` |
| Massive Crypto | `BTC-USD` (from `pair` field) | `BTC-USD` |
| Massive Forex | `EUR/USD` (from `p` field) | `EUR-USD` |

**Config Format:**
- Twelve Data: Use slash format (`BTC/USD`, `EUR/USD`)
- Massive: Use dash format (`BTC-USD`, `EUR-USD`) or `*` for all

## Production Considerations

1. **API Keys**: Store credentials securely (environment variables, secrets manager)
2. **Monitoring**: Integrate with Prometheus/Grafana for metrics
3. **Logging**: Configure structured logging for production
4. **TLS**: Enable TLS for WebSocket and TCP connections
5. **Rate Limiting**: Add rate limiting for public WebSocket endpoint
6. **Authentication**: Implement JWT or API key authentication
7. **Symbol Strategy**: Use explicit symbols for Twelve Data, wildcards for Massive to maximize coverage
8. **Failover Tuning**: Adjust `primary_silence_threshold_ms` based on your latency requirements

## Deployment Examples

**Local Development:**
```bash
./feed-core -config config.yaml
```

**Docker:**
```dockerfile
FROM golang:1.23-alpine
WORKDIR /app
COPY . .
RUN go build -o feed-core .
EXPOSE 8080 9000
CMD ["./feed-core"]
```

**Environment Variables (alternative to config file):**
```bash
export TWELVE_DATA_KEY="your_key"
export MASSIVE_KEY="your_key"
./feed-core
```

## License

MIT License
