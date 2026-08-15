//go:build windows

package main

// setupMaintenanceSignal is a no-op on Windows since SIGUSR1 is not available.
// The maintenance endpoint (POST /api/maintenance/pre-update) works on all platforms.
func setupMaintenanceSignal() {
}
