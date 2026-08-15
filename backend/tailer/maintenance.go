package tailer

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"logmara/sharedstate"
)

// globalMaintenanceState is set by main() after sharedstate.Client is ready.
// It tracks the pre-update maintenance lifecycle across replicas.
var globalMaintenanceState *sharedstate.MaintenanceState

// SetMaintenanceState stores the maintenance state for use by the handler.
func SetMaintenanceState(m *sharedstate.MaintenanceState) {
	globalMaintenanceState = m
}

// MaintenanceStatus returns the current maintenance status string.
func MaintenanceStatus() string {
	if globalMaintenanceState == nil {
		return sharedstate.MaintenanceIdle
	}
	return globalMaintenanceState.Status()
}

// PrepareForUpdate pauses ingestion, drains the RabbitMQ queue (HA mode),
// compacts the log file to zero, and resets the tailer position.
// It's designed to be called before a rolling update so that all replicas
// start from a clean slate after restart.
//
// In single-server mode: pauses ingestion loop, waits for final flush,
// compacts file, resets position.
//
// In HA mode: pauses FileReader (leader), waits for workers to finish
// current batch, purges remaining queue messages, resets flush tracker,
// truncates file, resets position.
func PrepareForUpdate(ctx context.Context, filePath string) error {
	globalMaintenanceState.SetPreparing()
	slog.Info("maintenance: pre-update preparation started")

	// Step 1: Pause ingestion globally.
	// FileReader (HA) checks ic.IsPaused() and stops publishing.
	// Ingestion loop (single-server) checks ic.IsPaused() and stops scanning.
	if currentIC != nil {
		currentIC.Pause()
		slog.Info("maintenance: ingestion paused")
	}

	// Step 2: Handle mode-specific drain.
	if currentPipeline != nil && currentPipeline.queue != nil {
		if err := prepareHA(ctx, filePath); err != nil {
			globalMaintenanceState.SetCompleted()
			return err
		}
	} else {
		if err := prepareSingleServer(ctx, filePath); err != nil {
			globalMaintenanceState.SetCompleted()
			return err
		}
	}

	globalMaintenanceState.SetCompleted()
	slog.Info("maintenance: pre-update preparation completed")
	return nil
}

// prepareHA handles the HA (RabbitMQ) path:
// 1. Wait for workers to finish current batch (~2s after pause)
// 2. Purge remaining queue messages
// 3. Reset flush tracker
// 4. Truncate log file
// 5. Reset position
func prepareHA(ctx context.Context, filePath string) error {
	// Wait for workers to finish their current batch.
	// After pause, workers flush the last batch, then nack+requeue
	// remaining messages. We give them ~2s to flush.
	slog.Info("maintenance: waiting for workers to finish current batch")
	if !sleepOrDone(ctx, 2*time.Second) {
		return ctx.Err()
	}

	// Purge remaining queue messages and reset flush tracker.
	// Messages in the queue have already been flushed to the DB
	// (workers flushed the last batch before starting to nack).
	msgs, errMsg := PurgeTailerQueue()
	if errMsg != "" {
		slog.Warn("maintenance: queue purge reported error", "error", errMsg)
	} else {
		slog.Info("maintenance: queue purged", "messages_removed", msgs)
	}

	// Truncate the log file.
	if err := truncateLogFile(filePath); err != nil {
		slog.Error("maintenance: failed to truncate log file", "error", err)
		return err
	}

	// Reset position.
	if err := resetPosition(filePath); err != nil {
		slog.Error("maintenance: failed to reset position", "error", err)
		return err
	}

	slog.Info("maintenance: HA preparation complete")
	return nil
}

// prepareSingleServer handles the single-server path:
// 1. Wait for ingestion loop to finish current batch (~2s after pause)
// 2. Compact file (already flushed up to flushedPos)
// 3. Reset position
func prepareSingleServer(ctx context.Context, filePath string) error {
	// Wait for the ingestion loop to finish its current batch.
	// After pause, the loop flushes remaining entries, then exits
	// the scan loop on the next IsPaused() check.
	slog.Info("maintenance: waiting for ingestion loop to finish")
	if !sleepOrDone(ctx, 2*time.Second) {
		return ctx.Err()
	}

	// Truncate the log file.
	if err := truncateLogFile(filePath); err != nil {
		slog.Error("maintenance: failed to truncate log file", "error", err)
		return err
	}

	// Reset position.
	if err := resetPosition(filePath); err != nil {
		slog.Error("maintenance: failed to reset position", "error", err)
		return err
	}

	slog.Info("maintenance: single-server preparation complete")
	return nil
}

// truncateLogFile truncates the log file to zero bytes and asks rsyslog
// to reopen the file handle.
func truncateLogFile(filePath string) error {
	slog.Info("maintenance: truncating log file", "path", filePath)

	// Write empty file atomically via temp file + rename.
	tmpPath := filePath + ".maintenance.tmp"
	if err := os.WriteFile(tmpPath, nil, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		os.Remove(tmpPath)
		return err
	}

	// Ask rsyslog to reopen the log file.
	if currentReopenLogFile != nil {
		if err := currentReopenLogFile(); err != nil {
			slog.Error("maintenance: failed to ask rsyslog to reopen", "error", err)
		} else {
			slog.Info("maintenance: rsyslog reopened log file")
		}
	}

	slog.Info("maintenance: log file truncated")
	return nil
}

// resetPosition clears the tailer position checkpoint in both the local
// file and Redis.
func resetPosition(filePath string) error {
	posFile := filepath.Join(filepath.Dir(filePath), positionFileName)

	// Clear local position file.
	if err := os.WriteFile(posFile, []byte("0:"), 0644); err != nil {
		slog.Warn("maintenance: failed to clear local position file", "error", err)
	}

	// Clear Redis position.
	if currentSharedClient != nil {
		currentSharedClient.ResetTailerPosition()
		slog.Info("maintenance: Redis position reset")
	}

	// Reset flush tracker (HA mode).
	if currentPipeline != nil && currentPipeline.flushTrk != nil {
		currentPipeline.flushTrk.Reset(context.Background())
		slog.Info("maintenance: flush tracker reset")
	}

	slog.Info("maintenance: position reset to 0")
	return nil
}

