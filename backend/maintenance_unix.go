//go:build !windows

package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"logmara/tailer"
)

func setupMaintenanceSignal() {
	sigusr1 := make(chan os.Signal, 1)
	signal.Notify(sigusr1, syscall.SIGUSR1)
	go func() {
		<-sigusr1
		slog.Info("maintenance: SIGUSR1 received, starting pre-update preparation")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := tailer.PrepareForUpdate(ctx, logFilePath); err != nil {
			slog.Error("maintenance: pre-update preparation failed", "error", err)
		} else {
			slog.Info("maintenance: pre-update preparation completed, safe to restart")
		}
	}()
}
