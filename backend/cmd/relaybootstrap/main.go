// Command relaybootstrap is a one-shot CLI wrapper around
// relaypki.EnsureCA - the single implementation of "generate/renew the
// relay CA and central mTLS server certificate", shared between the api
// service (which calls EnsureCA directly, in-process, from
// handler.SyncRelayConfig) and the rsyslog container, which has no Go
// runtime of its own and needs the CA/server cert to exist before it can
// bind its mTLS listener on 6515 - even on a first boot before the api
// service has necessarily run yet (see rsyslog/entrypoint.sh).
//
// EnsureCA is idempotent and safe to call repeatedly and concurrently with
// itself (writes are atomic temp-file-then-rename, and it only creates
// what's missing/renews what's expiring) - so having both the api service
// and this CLI race to call it on a brand-new deployment is harmless:
// whichever gets there first creates the CA/server cert, the other is a
// no-op, and either way there's exactly one code path involved.
package main

import (
	"fmt"
	"os"

	"logmara/relaypki"
)

func main() {
	dir := "/data/relay"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if err := relaypki.EnsureCA(dir); err != nil {
		fmt.Fprintln(os.Stderr, "relaybootstrap: ensure CA failed:", err)
		os.Exit(1)
	}
}
