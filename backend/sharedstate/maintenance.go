package sharedstate

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

const (
	maintenanceStatusKey = "maintenance:status"
	maintenanceStatusTTL = 24 * time.Hour
)

const (
	MaintenanceIdle     = "idle"
	MaintenancePreparing = "preparing"
	MaintenanceCompleted = "completed"
)

// maintenanceState tracks the pre-update maintenance lifecycle.
// It's shared across replicas via Redis so any replica can trigger
// or poll the status.
type MaintenanceState struct {
	status atomic.Int32
	client *Client
}

// status values (atomic.Int32)
const (
	statusIdle     = iota // 0
	statusPreparing       // 1
	statusCompleted       // 2
)

// NewMaintenanceState creates a maintenance state tracker. When client is nil
// (single-server deployment), it uses in-memory state. When client is set
// (HA deployment), it syncs state via Redis across replicas.
func NewMaintenanceState(client *Client) *MaintenanceState {
	m := &MaintenanceState{client: client}
	if client == nil {
		m.status.Store(statusIdle)
		return m
	}
	// Restore status from Redis
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	raw, err := client.Raw().Get(ctx, maintenanceStatusKey).Result()
	if err != nil {
		m.status.Store(statusIdle)
		return m
	}
	switch raw {
	case MaintenancePreparing:
		m.status.Store(statusPreparing)
	case MaintenanceCompleted:
		m.status.Store(statusCompleted)
	default:
		m.status.Store(statusIdle)
	}
	return m
}

func (m *MaintenanceState) Status() string {
	switch m.status.Load() {
	case statusPreparing:
		return MaintenancePreparing
	case statusCompleted:
		return MaintenanceCompleted
	default:
		return MaintenanceIdle
	}
}

func (m *MaintenanceState) SetPreparing() {
	m.status.Store(statusPreparing)
	m.writeStatus(MaintenancePreparing)
}

func (m *MaintenanceState) SetCompleted() {
	m.status.Store(statusCompleted)
	m.writeStatus(MaintenanceCompleted)
}

func (m *MaintenanceState) Clear() {
	m.status.Store(statusIdle)
	m.writeStatus(MaintenanceIdle)
}

func (m *MaintenanceState) writeStatus(status string) {
	if m.client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := m.client.Raw().Set(ctx, maintenanceStatusKey, status, maintenanceStatusTTL).Err(); err != nil {
		slog.Warn("maintenance: failed to write status to Redis", "status", status, "error", err)
	}
}
