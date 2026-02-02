package models

import (
	"encoding/json"
	"time"
)

// AssetType represents the type of financial asset
type AssetType string

const (
	Crypto AssetType = "CRYPTO"
	Forex  AssetType = "FOREX"
)

// SourceType represents the data source
type SourceType string

const (
	SourceTwelve          SourceType = "twelve"
	SourceMassive         SourceType = "massive"
	SourceTwelveFailover  SourceType = "twelve_failover"
	SourceMassiveFailover SourceType = "massive_failover"
)

// TickType distinguishes between quote and OHLC data
type TickType string

const (
	TickTypeQuote TickType = "quote"
	TickTypeOHLC  TickType = "ohlc"
)

// Tick represents a normalized price tick from any data source
// Supports both Quote data (p/b/a) and OHLC data (O/H/L/C)
// Fields use omitempty so only relevant fields are included in JSON output
type Tick struct {
	// Common fields
	Symbol    string     `json:"s"`           // Normalized: "BTC-USD"
	Timestamp int64      `json:"t"`           // Unix Microseconds
	Volume    float64    `json:"v,omitempty"` // Volume
	Type      AssetType  `json:"ty"`          // "CRYPTO" or "FOREX"
	Source    SourceType `json:"-"`           // Internal use only (not sent to clients)

	// Quote/Trade fields (for live data: XT, XQ, C events)
	Price float64 `json:"p,omitempty"` // Last trade price
	Bid   float64 `json:"b,omitempty"` // Best bid price
	Ask   float64 `json:"a,omitempty"` // Best ask price

	// OHLC fields (for aggregates: XA, XAS, CA, CAS events)
	Open  float64 `json:"O,omitempty"` // Open price
	High  float64 `json:"H,omitempty"` // High price
	Low   float64 `json:"L,omitempty"` // Low price
	Close float64 `json:"C,omitempty"` // Close price
}

// NewQuoteTick creates a new Quote Tick (for live price data)
func NewQuoteTick(symbol string, price, bid, ask, volume float64, source SourceType, assetType AssetType) *Tick {
	return &Tick{
		Symbol:    symbol,
		Price:     price,
		Bid:       bid,
		Ask:       ask,
		Volume:    volume,
		Timestamp: time.Now().UnixMicro(),
		Source:    source,
		Type:      assetType,
	}
}

// NewOHLCTick creates a new OHLC Tick (for aggregated data)
func NewOHLCTick(symbol string, open, high, low, close, volume float64, source SourceType, assetType AssetType) *Tick {
	return &Tick{
		Symbol:    symbol,
		Open:      open,
		High:      high,
		Low:       low,
		Close:     close,
		Volume:    volume,
		Timestamp: time.Now().UnixMicro(),
		Source:    source,
		Type:      assetType,
	}
}

// IsOHLC returns true if this tick contains OHLC data
func (t *Tick) IsOHLC() bool {
	return t.Open != 0 || t.High != 0 || t.Low != 0 || t.Close != 0
}

// ToJSON serializes the tick to JSON bytes
func (t *Tick) ToJSON() ([]byte, error) {
	return json.Marshal(t)
}

// FromJSON deserializes JSON bytes into a Tick
func FromJSON(data []byte) (*Tick, error) {
	var tick Tick
	if err := json.Unmarshal(data, &tick); err != nil {
		return nil, err
	}
	return &tick, nil
}

// ToTCPFrame creates JSON with newline for TCP transmission
func (t *Tick) ToTCPFrame() []byte {
	data, _ := json.Marshal(t)
	return append(data, '\n')
}

// Clone creates a deep copy of the tick
func (t *Tick) Clone() *Tick {
	return &Tick{
		Symbol:    t.Symbol,
		Price:     t.Price,
		Bid:       t.Bid,
		Ask:       t.Ask,
		Volume:    t.Volume,
		Timestamp: t.Timestamp,
		Source:    t.Source,
		Type:      t.Type,
		Open:      t.Open,
		High:      t.High,
		Low:       t.Low,
		Close:     t.Close,
	}
}

// Age returns the age of the tick in microseconds
func (t *Tick) Age() int64 {
	return time.Now().UnixMicro() - t.Timestamp
}
