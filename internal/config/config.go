package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config represents the complete application configuration
type Config struct {
	Server            ServerConfig        `yaml:"server"`
	Credentials       CredentialsConfig   `yaml:"credentials"`
	Settings          SettingsConfig      `yaml:"settings"`
	MassiveEvents     MassiveEventsConfig `yaml:"massive_events"`
	TwelveDataSymbols SymbolsConfig       `yaml:"twelve_data_symbols"`
	MassiveSymbols    SymbolsConfig       `yaml:"massive_symbols"`
}

// MassiveEventsConfig contains the event types to subscribe to for Massive
type MassiveEventsConfig struct {
	Crypto []string `yaml:"crypto"` // XT, XQ, XA, XAS
	Forex  []string `yaml:"forex"`  // C, CA, CAS
}

// ServerConfig contains server-related settings
type ServerConfig struct {
	Port    int `yaml:"port"`
	TCPPort int `yaml:"tcp_port"`
}

// CredentialsConfig contains API credentials
type CredentialsConfig struct {
	TwelveData string `yaml:"twelve_data"`
	Massive    string `yaml:"massive"`
}

// SettingsConfig contains operational settings
type SettingsConfig struct {
	PrimaryProvider           string `yaml:"primary_provider"` // "twelve" or "massive"
	PrimarySilenceThresholdMS int    `yaml:"primary_silence_threshold_ms"`
	ReconnectDelayMS          int    `yaml:"reconnect_delay_ms"`
	MaxReconnectAttempts      int    `yaml:"max_reconnect_attempts"`
}

// SymbolsConfig contains the symbol lists
type SymbolsConfig struct {
	Crypto []string `yaml:"crypto"`
	Forex  []string `yaml:"forex"`
}

// Load reads and parses the configuration from a YAML file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Set defaults
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Server.TCPPort == 0 {
		cfg.Server.TCPPort = 9000
	}
	if cfg.Settings.PrimaryProvider == "" {
		cfg.Settings.PrimaryProvider = "twelve"
	}
	if cfg.Settings.PrimarySilenceThresholdMS == 0 {
		cfg.Settings.PrimarySilenceThresholdMS = 500
	}
	if cfg.Settings.ReconnectDelayMS == 0 {
		cfg.Settings.ReconnectDelayMS = 1000
	}
	if cfg.Settings.MaxReconnectAttempts == 0 {
		cfg.Settings.MaxReconnectAttempts = 10
	}

	// Set default Massive events if not specified
	if len(cfg.MassiveEvents.Crypto) == 0 {
		cfg.MassiveEvents.Crypto = []string{"XT"} // Default to trades
	}
	if len(cfg.MassiveEvents.Forex) == 0 {
		cfg.MassiveEvents.Forex = []string{"C"} // Default to quotes
	}

	return &cfg, nil
}

// AllTwelveDataSymbols returns all Twelve Data symbols (crypto + forex)
func (c *Config) AllTwelveDataSymbols() []string {
	all := make([]string, 0, len(c.TwelveDataSymbols.Crypto)+len(c.TwelveDataSymbols.Forex))
	all = append(all, c.TwelveDataSymbols.Crypto...)
	all = append(all, c.TwelveDataSymbols.Forex...)
	return all
}

// AllMassiveSymbols returns all Massive symbols (crypto + forex)
func (c *Config) AllMassiveSymbols() []string {
	all := make([]string, 0, len(c.MassiveSymbols.Crypto)+len(c.MassiveSymbols.Forex))
	all = append(all, c.MassiveSymbols.Crypto...)
	all = append(all, c.MassiveSymbols.Forex...)
	return all
}

// Symbol Conversion Utilities

// ToTwelveFormat converts "BTC-USD" to "BTC/USD" (Twelve Data format)
func ToTwelveFormat(symbol string) string {
	return strings.ReplaceAll(symbol, "-", "/")
}

// NormalizeFromTwelve converts "BTC/USD" to "BTC-USD" (internal format)
func NormalizeFromTwelve(symbol string) string {
	return strings.ReplaceAll(symbol, "/", "-")
}

// NormalizeFromMassive converts "XT.BTC-USD" or "C.EUR-USD" to "BTC-USD" (internal format)
func NormalizeFromMassive(symbol string) string {
	// Strip "XT." or "C." prefix
	if strings.HasPrefix(symbol, "XT.") {
		return strings.TrimPrefix(symbol, "XT.")
	}
	if strings.HasPrefix(symbol, "C.") {
		return strings.TrimPrefix(symbol, "C.")
	}
	return symbol
}

// IsTwelveDataCrypto checks if a symbol is in Twelve Data crypto list
func (c *Config) IsTwelveDataCrypto(symbol string) bool {
	// Normalize to Twelve Data format (BTC/USD)
	twelveSymbol := ToTwelveFormat(symbol)
	for _, s := range c.TwelveDataSymbols.Crypto {
		if s == twelveSymbol || s == symbol {
			return true
		}
	}
	return false
}

// IsTwelveDataForex checks if a symbol is in Twelve Data forex list
func (c *Config) IsTwelveDataForex(symbol string) bool {
	// Normalize to Twelve Data format (EUR/USD)
	twelveSymbol := ToTwelveFormat(symbol)
	for _, s := range c.TwelveDataSymbols.Forex {
		if s == twelveSymbol || s == symbol {
			return true
		}
	}
	return false
}

// IsMassiveCrypto checks if a symbol is in Massive crypto list (handles wildcard)
func (c *Config) IsMassiveCrypto(symbol string) bool {
	for _, s := range c.MassiveSymbols.Crypto {
		if s == "*" || s == symbol {
			return true
		}
	}
	return false
}

// IsMassiveForex checks if a symbol is in Massive forex list (handles wildcard)
func (c *Config) IsMassiveForex(symbol string) bool {
	for _, s := range c.MassiveSymbols.Forex {
		if s == "*" || s == symbol {
			return true
		}
	}
	return false
}

// IsSymbolInTwelveData checks if a symbol (normalized to internal format) is configured for Twelve Data
func (c *Config) IsSymbolInTwelveData(normalizedSymbol string) bool {
	return c.IsTwelveDataCrypto(normalizedSymbol) || c.IsTwelveDataForex(normalizedSymbol)
}

// IsSymbolInMassive checks if a symbol (normalized to internal format) is configured for Massive
func (c *Config) IsSymbolInMassive(normalizedSymbol string) bool {
	return c.IsMassiveCrypto(normalizedSymbol) || c.IsMassiveForex(normalizedSymbol)
}

// IsSymbolInBothSources checks if a symbol exists in both data sources (failover eligible)
func (c *Config) IsSymbolInBothSources(normalizedSymbol string) bool {
	return c.IsSymbolInTwelveData(normalizedSymbol) && c.IsSymbolInMassive(normalizedSymbol)
}

// GetSymbolSourceInfo returns which sources have a symbol configured
// Returns: inTwelve, inMassive
func (c *Config) GetSymbolSourceInfo(normalizedSymbol string) (inTwelve, inMassive bool) {
	inTwelve = c.IsSymbolInTwelveData(normalizedSymbol)
	inMassive = c.IsSymbolInMassive(normalizedSymbol)
	return
}

// GetTwelveSymbols returns all Twelve Data symbols formatted for subscription
// (already in Twelve Data format: BTC/USD, EUR/USD)
func (c *Config) GetTwelveSymbols() []string {
	all := c.AllTwelveDataSymbols()
	result := make([]string, len(all))
	for i, s := range all {
		result[i] = ToTwelveFormat(s)
	}
	return result
}

// GetMassiveCryptoSymbols returns crypto subscriptions in Massive format
// Format: EVENT.SYMBOL (e.g., XT.BTC-USD, XQ.BTC-USD, XT.* for all)
func (c *Config) GetMassiveCryptoSymbols() []string {
	result := make([]string, 0, len(c.MassiveEvents.Crypto)*len(c.MassiveSymbols.Crypto))
	for _, event := range c.MassiveEvents.Crypto {
		for _, symbol := range c.MassiveSymbols.Crypto {
			result = append(result, event+"."+symbol)
		}
	}
	return result
}

// GetMassiveForexSymbols returns forex subscriptions in Massive format
// Format: EVENT.SYMBOL (e.g., C.EUR-USD, CA.EUR-USD, C.* for all)
func (c *Config) GetMassiveForexSymbols() []string {
	result := make([]string, 0, len(c.MassiveEvents.Forex)*len(c.MassiveSymbols.Forex))
	for _, event := range c.MassiveEvents.Forex {
		for _, symbol := range c.MassiveSymbols.Forex {
			result = append(result, event+"."+symbol)
		}
	}
	return result
}

// HasTwelveDataSymbols returns true if there are any Twelve Data symbols configured
func (c *Config) HasTwelveDataSymbols() bool {
	return len(c.TwelveDataSymbols.Crypto) > 0 || len(c.TwelveDataSymbols.Forex) > 0
}

// HasMassiveCryptoSymbols returns true if there are Massive crypto symbols configured
func (c *Config) HasMassiveCryptoSymbols() bool {
	return len(c.MassiveSymbols.Crypto) > 0
}

// HasMassiveForexSymbols returns true if there are Massive forex symbols configured
func (c *Config) HasMassiveForexSymbols() bool {
	return len(c.MassiveSymbols.Forex) > 0
}
