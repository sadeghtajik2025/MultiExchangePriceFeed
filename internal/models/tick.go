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

// Tick represents a normalized price tick from any data source
// This is the universal format used throughout the system
type Tick struct {
	Symbol    string     `json:"s"`    // Normalized: "BTC-USD"
	Price     float64    `json:"p"`    // Last trade price
	Bid       float64    `json:"b"`    // Best bid price (optional)
	Ask       float64    `json:"a"`    // Best ask price (optional)
	Volume    float64    `json:"v"`    // Volume (optional)
	Timestamp int64      `json:"t"`    // Unix Microseconds
	Source    SourceType `json:"src"`  // "twelve", "massive", or "massive_failover"
	Type      AssetType  `json:"type"` // "CRYPTO" or "FOREX"
}

// NewTick creates a new Tick with the current timestamp
func NewTick(symbol string, price float64, source SourceType, assetType AssetType) *Tick {
	return &Tick{
		Symbol:    symbol,
		Price:     price,
		Timestamp: time.Now().UnixMicro(),
		Source:    source,
		Type:      assetType,
	}
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

// ToTCPFrame creates a compact binary-like string for TCP transmission
// Format: SYMBOL|PRICE|TIMESTAMP|SOURCE\n
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
	}
}

// Age returns the age of the tick in microseconds
func (t *Tick) Age() int64 {
	return time.Now().UnixMicro() - t.Timestamp
}
