package control

import (
	"sync"
)

type IngestionController struct {
	mu     sync.RWMutex
	paused bool
}

func NewIngestionController() *IngestionController {
	return &IngestionController{}
}

func (ic *IngestionController) IsPaused() bool {
	ic.mu.RLock()
	defer ic.mu.RUnlock()
	return ic.paused
}

func (ic *IngestionController) Pause() {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	ic.paused = true
}

func (ic *IngestionController) Resume() {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	ic.paused = false
}

func (ic *IngestionController) Toggle() bool {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	ic.paused = !ic.paused
	return ic.paused
}