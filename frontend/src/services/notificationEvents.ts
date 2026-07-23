import type { InAppNotification } from './api'

type Listener = (n: InAppNotification) => void
const listeners = new Set<Listener>()

// NotificationBell owns the single SSE connection to /api/notifications/stream
// - other mounted components that want to react live (e.g. refreshing the
// Alert Rules / Alert History tabs as new alerts fire) subscribe here
// instead of each opening their own redundant stream.
export function onLiveNotification(listener: Listener): () => void {
	listeners.add(listener)
	return () => listeners.delete(listener)
}

export function emitLiveNotification(n: InAppNotification) {
	listeners.forEach((l) => l(n))
}
