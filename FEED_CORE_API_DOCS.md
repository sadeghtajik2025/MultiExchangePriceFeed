# Feed Core API Reference

**Server:** `5.75.180.24`  
**Port:** `8080`  
**Protocol:** WebSocket + HTTP

---

## HTTP Endpoints

### Health Check
```
GET http://5.75.180.24:8080/health
```

**Response:**
```json
{
  "status": "healthy",
  "clients": 1,
  "ticks": 100111604
}
```

### Stats
```
GET http://5.75.180.24:8080/stats
```

**Response:**
```json
{
  "connected_clients": 1,
  "total_ticks": 100111604,
  "client_ids": ["tcp-123-1", "ws-456-2"]
}
```

---

## WebSocket Connection

### URL
```
ws://5.75.180.24:8080/ws
```

### Connection
- No authentication required
- Connect and immediately start receiving all ticks
- No subscription message needed

---

## Tick Data Formats

### Quote Tick (Live Trades/Quotes)

```json
{
  "s": "BTC-USD",
  "p": 78000.50,
  "b": 78000.00,
  "a": 78001.00,
  "v": 0.5,
  "t": 1770761234721000,
  "ty": "CRYPTO"
}
```

### OHLC Tick (Aggregates)

```json
{
  "s": "BTC-USD",
  "O": 78000.00,
  "H": 78100.00,
  "L": 77900.00,
  "C": 78050.00,
  "v": 125.5,
  "t": 1770761234721000,
  "ty": "CRYPTO"
}
```

---

## Field Reference

| Field | Type | Description |
|-------|------|-------------|
| `s` | string | Symbol (e.g., "BTC-USD", "ETH-USD") |
| `p` | float | Last trade price |
| `b` | float | Best bid price |
| `a` | float | Best ask price |
| `O` | float | Open price (OHLC only) |
| `H` | float | High price (OHLC only) |
| `L` | float | Low price (OHLC only) |
| `C` | float | Close price (OHLC only) |
| `v` | float | Volume |
| `t` | int | Timestamp in microseconds (Unix epoch) |
| `ty` | string | Asset type: "CRYPTO" or "FOREX" |

---

## Code Examples

### Node.js

```javascript
const WebSocket = require('ws');

const ws = new WebSocket('ws://5.75.180.24:8080/ws');

ws.on('open', () => {
  console.log('Connected to Feed Core');
});

ws.on('message', (data) => {
  const tick = JSON.parse(data);
  console.log(`${tick.s}: ${tick.p}`);
});

ws.on('close', () => {
  console.log('Disconnected');
});

ws.on('error', (err) => {
  console.error('Error:', err.message);
});
```

### Browser JavaScript

```javascript
const ws = new WebSocket('ws://5.75.180.24:8080/ws');

ws.onopen = () => console.log('Connected');

ws.onmessage = (event) => {
  const tick = JSON.parse(event.data);
  console.log(tick.s, tick.p);
};

ws.onclose = () => console.log('Disconnected');
```

### Python

```python
import websocket
import json

def on_message(ws, message):
    tick = json.loads(message)
    print(f"{tick['s']}: {tick['p']}")

def on_open(ws):
    print("Connected")

ws = websocket.WebSocketApp(
    "ws://5.75.180.24:8080/ws",
    on_message=on_message,
    on_open=on_open
)
ws.run_forever()
```

### cURL (Health Check)

```bash
curl http://5.75.180.24:8080/health
```

---

## Available Symbols

| Type | Examples |
|------|----------|
| Crypto | BTC-USD, ETH-USD, LINK-USD |
| Forex | EUR-USD, GBP-USD |

---

## Notes

1. All timestamps are in **microseconds** (Unix epoch)
2. Quote ticks have `p`, `b`, `a` fields
3. OHLC ticks have `O`, `H`, `L`, `C` fields
4. Both types include `s`, `t`, `ty`, and optionally `v`
5. Connection is persistent - no ping/pong required from client

---

*Document Version: 1.0*  
*Last Updated: February 2026*
