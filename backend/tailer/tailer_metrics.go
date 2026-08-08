package tailer

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"logmara/sharedstate"
)

const tailerMetricsRedisKey = "tailer:metrics"
const tailerMetricsReplicaKey = "tailer:replicas"
const tailerMetricsTTL = 10 * time.Second
const tailerMetricsReplicaTTL = 30 * time.Second

// TailerMetrics aggregates global and per-worker tailer metrics.
type TailerMetrics struct {
	mu             sync.RWMutex
	NumWorkers     int
	QueueDepth     int64
	FlushedPos     int64
	FlushedSeq     int64
	LogsPerSec     float64
	WorkerMetrics  []WorkerMetrics
	UpdatedAt      time.Time
}

// tailerMetricsPublic is a JSON-serializable snapshot of TailerMetrics
// stored in Redis so non-leader replicas can serve the admin endpoint.
type tailerMetricsPublic struct {
	NumWorkers    int                   `json:"num_workers"`
	QueueDepth    int64                 `json:"queue_depth"`
	FlushedPos    int64                 `json:"flushed_pos"`
	FlushedSeq    int64                 `json:"flushed_seq"`
	LogsPerSec    float64               `json:"logs_per_sec"`
	WorkerMetrics []WorkerMetricsPublic `json:"worker_metrics"`
	UpdatedAt     time.Time             `json:"updated_at"`
}

// AggregatedTailerMetrics holds aggregated tailer metrics from all API replicas.
type AggregatedTailerMetrics struct {
	PipelineActive bool
	NumWorkers     int
	QueueDepth     int64
	FlushedPos     int64
	FlushedSeq     int64
	LogsPerSec     float64
	WorkerMetrics  []WorkerMetrics
	Replicas       []ReplicaTailerMetrics
	UpdatedAt      time.Time
}

// ReplicaTailerMetrics holds per-replica tailer metrics.
type ReplicaTailerMetrics struct {
	NodeID        string
	NumWorkers    int
	QueueDepth    int64
	FlushedPos    int64
	FlushedSeq    int64
	LogsPerSec    float64
	WorkerMetrics []WorkerMetrics
	UpdatedAt     time.Time
}

// TailerMetricsCollector periodically updates TailerMetrics from live sources.
type TailerMetricsCollector struct {
	metrics  *TailerMetrics
	queue    *sharedstate.Queue
	flushTrk *sharedstate.FlushTracker
	rate     sharedstate.RateCounter
	pool     *WorkerPool
	client   *sharedstate.Client
	nodeID   string
	stop     chan struct{}
}

func NewTailerMetricsCollector(queue *sharedstate.Queue, flushTrk *sharedstate.FlushTracker,
	rate sharedstate.RateCounter, pool *WorkerPool, client *sharedstate.Client, nodeID string) *TailerMetricsCollector {

	return &TailerMetricsCollector{
		metrics:  &TailerMetrics{},
		queue:    queue,
		flushTrk: flushTrk,
		rate:     rate,
		pool:     pool,
		client:   client,
		nodeID:   nodeID,
		stop:     make(chan struct{}),
	}
}

func (c *TailerMetricsCollector) Start(ctx context.Context) {
	if c.client != nil {
		_, err := c.client.Raw().SAdd(ctx, tailerMetricsReplicaKey, c.nodeID).Result()
		if err != nil {
			slog.Warn("tailer metrics: failed to register replica", "node", c.nodeID, "error", err)
		} else {
			c.client.Raw().Expire(ctx, tailerMetricsReplicaKey, tailerMetricsReplicaTTL)
		}
	}
	go c.updateLoop(ctx)
}

func (c *TailerMetricsCollector) Stop() {
	if c.client != nil {
		c.client.Raw().SRem(context.Background(), tailerMetricsReplicaKey, c.nodeID)
		c.client.Raw().Del(context.Background(), replicaMetricsKey(c.nodeID))
	}
	close(c.stop)
}

func (c *TailerMetricsCollector) Get() TailerMetrics {
	c.metrics.mu.RLock()
	defer c.metrics.mu.RUnlock()
	return *c.metrics
}

func (c *TailerMetricsCollector) updateLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stop:
			return
		case <-ticker.C:
			c.update(ctx)
		}
	}
}

func replicaMetricsKey(nodeID string) string {
	return "tailer:metrics:" + nodeID
}

func (c *TailerMetricsCollector) update(ctx context.Context) {
	queueDepth := c.queue.Len(ctx)
	flushedSeq, flushedPos := c.flushTrk.GetFlushedPos(ctx)
	logsPerSec := c.rate.Rate(ctx, 60)

	workerMetrics := c.pool.GetMetrics()

	m := tailerMetricsPublic{
		NumWorkers:    c.pool.NumWorkers(),
		QueueDepth:    queueDepth,
		FlushedPos:    flushedPos,
		FlushedSeq:    flushedSeq,
		LogsPerSec:    logsPerSec,
		WorkerMetrics: c.pool.GetPublicMetrics(),
		UpdatedAt:     time.Now(),
	}

	c.metrics.mu.Lock()
	c.metrics.QueueDepth = queueDepth
	c.metrics.FlushedPos = flushedPos
	c.metrics.FlushedSeq = flushedSeq
	c.metrics.LogsPerSec = logsPerSec
	c.metrics.WorkerMetrics = workerMetrics
	c.metrics.UpdatedAt = time.Now()
	c.metrics.mu.Unlock()

	// Persist per-replica metrics to Redis so the admin endpoint can aggregate
	if c.client != nil {
		data, err := json.Marshal(m)
		if err == nil {
			c.client.Raw().Set(ctx, replicaMetricsKey(c.nodeID), data, tailerMetricsTTL)
		}
	}
}
