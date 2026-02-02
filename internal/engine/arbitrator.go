package engine

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	"feed-core/internal/config"
	"feed-core/internal/models"
)

// Arbitrator implements the "Silence Detector" failover strategy
// It prioritizes the configured primary provider and uses the other as backup
// when the primary has been silent for longer than the configured threshold
type Arbitrator struct {
	cfg                    *config.Config
	inputChan              <-chan *models.Tick // Receives ticks from adapters
	outputChan             chan<- *models.Tick // Sends winner ticks to hub
	lastPrimaryTick        map[string]int64    // Last seen timestamp per symbol (microseconds)
	silenceThresholdMicros int64               // Converted threshold in microseconds
	primarySource          models.SourceType   // Primary data source
	backupSource           models.SourceType   // Backup data source
	failoverSource         models.SourceType   // Failover source tag (e.g., "twelve_failover" or "massive_failover")
	mu                     sync.RWMutex
	stopChan               chan struct{}
	stats                  *ArbitratorStats
}

// ArbitratorStats tracks arbitration metrics
type ArbitratorStats struct {
	PrimaryTicksProcessed int64
	BackupTicksProcessed  int64
	BackupTicksDropped    int64
	FailoversTriggered    int64
	mu                    sync.RWMutex
}

// NewArbitrator creates a new Arbitrator instance
func NewArbitrator(cfg *config.Config, inputChan <-chan *models.Tick, outputChan chan<- *models.Tick) *Arbitrator {
	// Determine primary, backup, and failover source tags based on config
	var primarySource, backupSource, failoverSource models.SourceType
	if cfg.Settings.PrimaryProvider == "massive" {
		primarySource = models.SourceMassive
		backupSource = models.SourceTwelve
		failoverSource = models.SourceTwelveFailover // When massive is primary, twelve is the failover
	} else {
		// Default: twelve is primary
		primarySource = models.SourceTwelve
		backupSource = models.SourceMassive
		failoverSource = models.SourceMassiveFailover // When twelve is primary, massive is the failover
	}

	return &Arbitrator{
		cfg:                    cfg,
		inputChan:              inputChan,
		outputChan:             outputChan,
		lastPrimaryTick:        make(map[string]int64),
		silenceThresholdMicros: int64(cfg.Settings.PrimarySilenceThresholdMS) * 1000, // ms to micros
		primarySource:          primarySource,
		backupSource:           backupSource,
		failoverSource:         failoverSource,
		stopChan:               make(chan struct{}),
		stats:                  &ArbitratorStats{},
	}
}

// Start begins processing ticks
func (a *Arbitrator) Start() {
	go a.processLoop()
	log.Printf("[Arbitrator] Started with primary=%s, backup=%s, silence threshold=%dms",
		a.primarySource, a.backupSource, a.cfg.Settings.PrimarySilenceThresholdMS)
}

// Stop gracefully shuts down the arbitrator
func (a *Arbitrator) Stop() {
	close(a.stopChan)
	log.Println("[Arbitrator] Stopped")
}

// GetStats returns current arbitration statistics
func (a *Arbitrator) GetStats() ArbitratorStats {
	a.stats.mu.RLock()
	defer a.stats.mu.RUnlock()
	return ArbitratorStats{
		PrimaryTicksProcessed: a.stats.PrimaryTicksProcessed,
		BackupTicksProcessed:  a.stats.BackupTicksProcessed,
		BackupTicksDropped:    a.stats.BackupTicksDropped,
		FailoversTriggered:    a.stats.FailoversTriggered,
	}
}

// processLoop is the main arbitration loop
func (a *Arbitrator) processLoop() {
	for {
		select {
		case <-a.stopChan:
			return
		case tick, ok := <-a.inputChan:
			if !ok {
				return
			}
			a.processTick(tick)
		}
	}
}

// processTick implements the silence detector logic
func (a *Arbitrator) processTick(tick *models.Tick) {
	if tick == nil {
		return
	}

	currentMicros := time.Now().UnixMicro()

	if tick.Source == a.primarySource {
		// Primary source - always publish
		a.handlePrimaryTick(tick, currentMicros)
	} else if tick.Source == a.backupSource {
		// Backup source - only publish during failover
		a.handleBackupTick(tick, currentMicros)
	} else {
		// Unknown source, log and drop
		log.Printf("[Arbitrator] Unknown source: %s for symbol %s", tick.Source, tick.Symbol)
	}
}

// handlePrimaryTick processes ticks from the primary source
func (a *Arbitrator) handlePrimaryTick(tick *models.Tick, currentMicros int64) {
	// Update the last seen timestamp for this symbol
	a.mu.Lock()
	a.lastPrimaryTick[tick.Symbol] = currentMicros
	a.mu.Unlock()

	// Update stats
	a.stats.mu.Lock()
	a.stats.PrimaryTicksProcessed++
	a.stats.mu.Unlock()

	// Always publish primary source immediately
	a.publishTick(tick)
}

// handleBackupTick processes ticks from the backup source
func (a *Arbitrator) handleBackupTick(tick *models.Tick, currentMicros int64) {
	// Check if this symbol is configured for both sources (failover eligible)
	inTwelve, inMassive := a.cfg.GetSymbolSourceInfo(tick.Symbol)

	// Determine if symbol exists in primary source
	symbolInPrimary := false
	if a.primarySource == models.SourceTwelve {
		symbolInPrimary = inTwelve
	} else {
		symbolInPrimary = inMassive
	}

	// If symbol is NOT in primary source, accept backup tick directly (no failover needed)
	// This symbol is ONLY available from the backup source
	if !symbolInPrimary {
		// Update stats
		a.stats.mu.Lock()
		a.stats.BackupTicksProcessed++
		a.stats.mu.Unlock()

		// Publish directly without changing source (it's the only source for this symbol)
		a.publishTick(tick)
		return
	}

	// Symbol exists in both sources - apply failover logic
	// Check how long since we heard from primary source for this symbol
	a.mu.RLock()
	lastSeen, exists := a.lastPrimaryTick[tick.Symbol]
	a.mu.RUnlock()

	var silenceDuration int64
	if exists {
		silenceDuration = currentMicros - lastSeen
	} else {
		// Never heard from primary for this symbol (even though it's configured)
		// Consider it as maximum silence to allow backup through
		silenceDuration = a.silenceThresholdMicros + 1
	}

	// FAILOVER CHECK
	if silenceDuration > a.silenceThresholdMicros {
		// Primary has been silent too long - failover to backup
		tick.Source = a.failoverSource // Tag it as failover (dynamically based on config)

		// Update stats
		a.stats.mu.Lock()
		a.stats.BackupTicksProcessed++
		a.stats.FailoversTriggered++
		a.stats.mu.Unlock()

		a.publishTick(tick)

		// Log failover (rate-limited to avoid log spam)
		if a.stats.FailoversTriggered%100 == 1 {
			log.Printf("[Arbitrator] Failover active for %s (%s silent for %dms)",
				tick.Symbol, a.primarySource, silenceDuration/1000)
		}
	} else {
		// Primary is healthy - drop the backup tick to save bandwidth
		a.stats.mu.Lock()
		a.stats.BackupTicksDropped++
		a.stats.mu.Unlock()
	}
}

// publishTick sends the tick to the output channel (Hub)
func (a *Arbitrator) publishTick(tick *models.Tick) {
	// Log every 100th tick to show data is flowing (avoid log spam)
	tickCount := atomic.LoadInt64(&a.stats.PrimaryTicksProcessed) + atomic.LoadInt64(&a.stats.BackupTicksProcessed)
	if tickCount%100 == 1 {
		log.Printf("[Price] %s: %.5f (source: %s)", tick.Symbol, tick.Price, tick.Source)
	}

	select {
	case a.outputChan <- tick:
		// Tick sent successfully
	default:
		// Output channel full, log warning
		log.Printf("[Arbitrator] Output channel full, dropping tick for %s", tick.Symbol)
	}
}

// GetSymbolStatus returns the failover status for a specific symbol
func (a *Arbitrator) GetSymbolStatus(symbol string) (isHealthy bool, silenceDurationMS int64) {
	a.mu.RLock()
	lastSeen, exists := a.lastPrimaryTick[symbol]
	a.mu.RUnlock()

	if !exists {
		return false, -1
	}

	silenceDuration := time.Now().UnixMicro() - lastSeen
	isHealthy = silenceDuration <= a.silenceThresholdMicros
	silenceDurationMS = silenceDuration / 1000

	return isHealthy, silenceDurationMS
}

// GetAllSymbolStatuses returns the health status of all tracked symbols
func (a *Arbitrator) GetAllSymbolStatuses() map[string]bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	currentMicros := time.Now().UnixMicro()
	statuses := make(map[string]bool, len(a.lastPrimaryTick))

	for symbol, lastSeen := range a.lastPrimaryTick {
		silenceDuration := currentMicros - lastSeen
		statuses[symbol] = silenceDuration <= a.silenceThresholdMicros
	}

	return statuses
}
