package sharedstate

import (
	"context"
	"log/slog"
	"strconv"
	"sync"

	"github.com/redis/go-redis/v9"
)

const flushTrackerKey = "tailer:flush:tracker"

// FlushTracker provides a distributed flush tracker backed by Redis.
// It tracks which file positions have been flushed by workers across all
// replicas, allowing the leader to advance the saved position only up to
// the last contiguous flushed position.
//
// The tracker uses a Redis HASH with sequence numbers as keys and file
// positions as values. A Lua script ensures atomic report-and-advance:
// workers report their flushed position, and the script atomically
// advances the flushed sequence counter to the last contiguous value.
type FlushTracker struct {
	client *Client
	seq    int64
	mu     sync.Mutex
	script *redis.Script
}

// reportFlushedLua is a Lua script that atomically:
// 1. HSETNX the flushed position for each reported sequence
// 2. Advances flushedSeq to the last contiguous flushed sequence
// 3. Returns the current flushedSeq and flushedPos
const reportFlushedLua = `
local key = KEYS[1]
local flushedSeq = tonumber(redis.call('HGET', key, '_flushed_seq')) or 0
local flushedPos = tonumber(redis.call('HGET', key, '_flushed_pos')) or 0

-- Store reported positions
for i = 2, #ARGV, 2 do
    local seq = ARGV[i]
    local pos = ARGV[i + 1]
    redis.call('HSETNX', key, seq, pos)
end

-- Advance flushedSeq contiguously
local nextSeq = flushedSeq + 1
while true do
    local nextPos = redis.call('HGET', key, nextSeq)
    if not nextPos then
        break
    end
    flushedSeq = nextSeq
    flushedPos = nextPos
    nextSeq = flushedSeq + 1
end

-- Persist flushed state
redis.call('HSET', key, '_flushed_seq', flushedSeq)
redis.call('HSET', key, '_flushed_pos', flushedPos)

return {flushedSeq, flushedPos}
`

func NewFlushTracker(client *Client) *FlushTracker {
	return &FlushTracker{
		client: client,
		script: redis.NewScript(reportFlushedLua),
	}
}

// QueueEntry carries the metadata needed to track flush progress.
type QueueEntry struct {
	Seq     int64  `json:"seq"`
	NextPos int64  `json:"next_pos"`
	Line    string `json:"line"`
}

// ReportFlushed atomically reports that the given entries have been flushed
// to the database. It returns the current contiguous flushed position.
func (ft *FlushTracker) ReportFlushed(ctx context.Context, entries []QueueEntry) (flushedSeq int64, flushedPos int64, err error) {
	if len(entries) == 0 {
		seq, pos := ft.GetFlushedPos(ctx)
		return seq, pos, nil
	}

	// Build arguments: seq1, pos1, seq2, pos2, ...
	args := make([]string, 0, 2*len(entries)+1)
	args = append(args, flushTrackerKey)
	for _, e := range entries {
		args = append(args, strconv.FormatInt(e.Seq, 10), strconv.FormatInt(e.NextPos, 10))
	}

	res, err := ft.script.Run(ctx, ft.client.rdb, args).Result()
	if err != nil {
		slog.Error("flush tracker: report error", "error", err)
		return 0, 0, err
	}

	results, ok := res.([]interface{})
	if !ok || len(results) < 2 {
		seq, pos := ft.GetFlushedPos(ctx)
		return seq, pos, nil
	}

	flushedSeq = redisToInt64(results[0])
	flushedPos = redisToInt64(results[1])
	return flushedSeq, flushedPos, nil
}

// GetFlushedPos returns the current contiguous flushed position.
func (ft *FlushTracker) GetFlushedPos(ctx context.Context) (int64, int64) {
	res, err := ft.client.rdb.HMGet(ctx, flushTrackerKey, "_flushed_seq", "_flushed_pos").Result()
	if err != nil || len(res) < 2 {
		return 0, 0
	}
	seq := redisToInt64(res[0])
	pos := redisToInt64(res[1])
	return seq, pos
}

// Reset clears the flush tracker state (used during compaction).
func (ft *FlushTracker) Reset(ctx context.Context) {
	ft.client.rdb.Del(ctx, flushTrackerKey)
	ft.mu.Lock()
	ft.seq = 0
	ft.mu.Unlock()
}

// NextSeq atomically allocates the next sequence number.
func (ft *FlushTracker) NextSeq() int64 {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.seq++
	return ft.seq
}

// redisToInt64 converts a Redis result value to int64. Lua scripts return
// integers as int64, while HMGet returns strings — handle both.
func redisToInt64(v interface{}) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case int:
		return int64(val)
	case string:
		n, _ := strconv.ParseInt(val, 10, 64)
		return n
	default:
		return 0
	}
}
