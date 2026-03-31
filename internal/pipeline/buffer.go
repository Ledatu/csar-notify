package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/ledatu/csar-notify/internal/domain"
	"github.com/ledatu/csar-notify/internal/rmq"
	"github.com/prometheus/client_golang/prometheus"
)

var ErrFull = errors.New("notify pipeline buffer full")

type BufferMetrics struct {
	EventsPublished prometheus.Counter
	EventsDropped   *prometheus.CounterVec
}

type Buffer struct {
	ch       chan *domain.Notification
	capacity int
	pub      *rmq.Publisher
	logger   *slog.Logger
	metrics  *BufferMetrics

	wg sync.WaitGroup
}

func NewBuffer(depth int, workers int, pub *rmq.Publisher, logger *slog.Logger, m *BufferMetrics) *Buffer {
	if depth < 1 {
		depth = 1
	}
	if workers < 1 {
		workers = 1
	}
	if logger == nil {
		logger = slog.Default()
	}
	b := &Buffer{
		ch:       make(chan *domain.Notification, depth),
		capacity: depth,
		pub:      pub,
		logger:   logger.With("component", "notify_pipeline"),
		metrics:  m,
	}
	for range workers {
		b.wg.Add(1)
		go b.worker()
	}
	return b
}

func (b *Buffer) Depth() int {
	return len(b.ch)
}

func (b *Buffer) Capacity() int {
	return b.capacity
}

func (b *Buffer) Submit(n *domain.Notification) error {
	if n == nil {
		return errors.New("nil notification")
	}
	select {
	case b.ch <- n:
		return nil
	default:
		b.logger.Warn("notify buffer full, notification dropped", "topic", n.Topic, "sender", n.Sender)
		if b.metrics != nil {
			b.metrics.EventsDropped.WithLabelValues("buffer_full").Inc()
		}
		return ErrFull
	}
}

func (b *Buffer) worker() {
	defer b.wg.Done()
	for n := range b.ch {
		b.publishWithRetry(n)
	}
}

func (b *Buffer) publishWithRetry(n *domain.Notification) {
	if n == nil {
		return
	}
	body, err := json.Marshal(n)
	if err != nil {
		b.logger.Error("notify encode failed", "error", err)
		if b.metrics != nil {
			b.metrics.EventsDropped.WithLabelValues("encode_error").Inc()
		}
		return
	}

	const maxAttempts = 4
	backoff := 100 * time.Millisecond
	for attempt := range maxAttempts {
		if attempt > 0 {
			time.Sleep(backoff)
			backoff = min(backoff*2, 5*time.Second)
		}
		pctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err = b.pub.PublishRaw(pctx, body)
		cancel()
		if err == nil {
			if b.metrics != nil {
				b.metrics.EventsPublished.Inc()
			}
			return
		}
		b.logger.Warn("notify publish failed", "error", err, "attempt", attempt+1, "topic", n.Topic)
	}

	b.logger.Error("notify publish exhausted retries", "error", err, "topic", n.Topic)
	if b.metrics != nil {
		b.metrics.EventsDropped.WithLabelValues("publish_error").Inc()
	}
}

func (b *Buffer) Close() {
	close(b.ch)
	b.wg.Wait()
}
