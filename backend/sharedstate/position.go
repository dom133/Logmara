package sharedstate

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

const (
	tailerPosKey     = "tailer:position"
	tailerPosTTL     = 24 * time.Hour
	tailerPosVersion = 1
)

// tailerPosRecord is the JSON-serialized structure stored in Redis at
// tailerPosKey. The version field lets us invalidate stale records from
// a future format change without losing the ability to read them.
type tailerPosRecord struct {
	Version     int    `json:"v"`
	Position    int64  `json:"pos"`
	Fingerprint string `json:"fp"`
}

// SaveTailerPosition writes pos+fp to Redis. When client is nil (single-
// replica deployment) this is a no-op. The write is fire-and-forget:
// a transient Redis error here only means a future leader handoff will
// fall back to the slower NFS-file or DB-based recovery path.
func (c *Client) SaveTailerPosition(pos int64, fp string) {
	if c == nil {
		return
	}
	rec := tailerPosRecord{
		Version:     tailerPosVersion,
		Position:    pos,
		Fingerprint: fp,
	}
	data, err := json.Marshal(rec)
	if err != nil {
		slog.Warn("tailer position: marshal error", "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Raw().Set(ctx, tailerPosKey, string(data), tailerPosTTL).Err(); err != nil {
		slog.Warn("tailer position: Redis write error", "error", err)
	}
}

// ResetTailerPosition removes the tailer position key from Redis.
// Call during a full purge so the tailer restarts from 0.
func (c *Client) ResetTailerPosition() {
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Raw().Del(ctx, tailerPosKey).Err(); err != nil {
		slog.Warn("tailer position: Redis delete error", "error", err)
	}
}

// LoadTailerPosition reads the last saved position+fp from Redis. Returns
// (0, 0, false) when client is nil or the key is missing/stale.
func (c *Client) LoadTailerPosition() (pos, flushedPos int64, ok bool) {
	if c == nil {
		return 0, 0, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	raw, err := c.Raw().Get(ctx, tailerPosKey).Result()
	if err != nil {
		return 0, 0, false
	}
	var rec tailerPosRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		slog.Warn("tailer position: Redis read parse error", "error", err)
		return 0, 0, false
	}
	if rec.Version != tailerPosVersion {
		slog.Warn("tailer position: version mismatch, ignoring", "have", rec.Version, "want", tailerPosVersion)
		return 0, 0, false
	}
	if rec.Position < 0 {
		slog.Warn("tailer position: negative position from Redis, ignoring", "pos", rec.Position)
		return 0, 0, false
	}
	slog.Info("restored tailer position from Redis", "pos", rec.Position)
	return rec.Position, rec.Position, true
}
