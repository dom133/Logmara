package sharedstate

import (
	"context"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	rabbitmqExchange = "" // default direct exchange
	rabbitmqQueue    = "tailer-ingestion"
	rabbitmqPrefetch = 1000
	rabbitmqMaxLen   = 50000
	rabbitmqReconnectInterval = 5 * time.Second
)

// Queue wraps a RabbitMQ connection, channel, and queue for the tailer
// ingestion pipeline. It provides publish/consume with auto-reconnect.
type Queue struct {
	mu       sync.RWMutex
	url      string
	conn     *amqp.Connection
	channel  *amqp.Channel
	queue    amqp.Queue
	closed   bool
}

func NewQueue(url string) (*Queue, error) {
	q := &Queue{url: url}
	if err := q.connect(); err != nil {
		return nil, err
	}
	return q, nil
}

func (q *Queue) connect() error {
	var err error
	q.conn, err = amqp.Dial(q.url)
	if err != nil {
		return err
	}
	q.channel, err = q.conn.Channel()
	if err != nil {
		q.conn.Close()
		return err
	}
	// Declare queue with durable=false (in-memory only, tailer data lives in the file)
	q.queue, err = q.channel.QueueDeclare(
		rabbitmqQueue,
		false, // durable
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		q.channel.Close()
		q.conn.Close()
		return err
	}
	return nil
}

func (q *Queue) ensureConnected() error {
	q.mu.RLock()
	if q.conn != nil && !q.conn.IsClosed() {
		q.mu.RUnlock()
		return nil
	}
	q.mu.RUnlock()

	q.mu.Lock()
	defer q.mu.Unlock()
	if q.conn != nil && !q.conn.IsClosed() {
		return nil
	}
	return q.reconnectLocked()
}

func (q *Queue) reconnectLocked() error {
	if q.conn != nil {
		q.conn.Close()
	}
	if err := q.connect(); err != nil {
		slog.Error("queue: reconnect failed", "error", err)
		return err
	}
	slog.Info("queue: reconnected to RabbitMQ")
	return nil
}

// Publish sends a single message to the ingestion queue.
func (q *Queue) Publish(ctx context.Context, data []byte) error {
	if err := q.ensureConnected(); err != nil {
		return err
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.channel.PublishWithContext(
		ctx,
		rabbitmqExchange,
		rabbitmqQueue,
		false, // mandatory
		false, // immediate
		amqp.Publishing{
			ContentType: "application/json",
			Body:        data,
		},
	)
}

// Consume starts consuming messages from the queue. Returns a channel that
// delivers messages to the caller. The caller must ACK/NACK each delivery.
func (q *Queue) Consume(ctx context.Context) (<-chan amqp.Delivery, error) {
	if err := q.ensureConnected(); err != nil {
		return nil, err
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	if err := q.channel.Qos(rabbitmqPrefetch, 0, false); err != nil {
		return nil, err
	}
	return q.channel.Consume(
		rabbitmqQueue,
		"", // consumer tag (auto-generated)
		false, // auto-ack
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,   // args
	)
}

// Len returns the approximate number of messages waiting in the queue.
func (q *Queue) Len(ctx context.Context) int64 {
	if err := q.ensureConnected(); err != nil {
		return 0
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	return int64(q.queue.Messages)
}

// IsFull returns true if the queue has reached maxLen messages (backpressure).
func (q *Queue) IsFull(ctx context.Context) bool {
	return q.Len(ctx) >= rabbitmqMaxLen
}

// Close shuts down the RabbitMQ connection.
func (q *Queue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	var err error
	if q.channel != nil {
		err = q.channel.Close()
	}
	if q.conn != nil {
		if cErr := q.conn.Close(); cErr != nil {
			err = cErr
		}
	}
	return err
}
