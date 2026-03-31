package sse

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ledatu/csar-core/gatewayctx"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

type Hub struct {
	redis  *redis.Client
	prefix string
	logger *slog.Logger
	gauge  prometheus.Gauge

	mu      sync.RWMutex
	clients map[string]map[chan []byte]struct{}
}

func New(redisClient *redis.Client, prefix string, logger *slog.Logger, gauge prometheus.Gauge) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	return &Hub{
		redis:   redisClient,
		prefix:  prefix,
		logger:  logger.With("component", "notify_sse"),
		gauge:   gauge,
		clients: make(map[string]map[chan []byte]struct{}),
	}
}

func (h *Hub) Start(ctx context.Context) error {
	if h.redis == nil {
		return nil
	}
	pubsub := h.redis.PSubscribe(ctx, h.prefix+"*")
	if _, err := pubsub.Receive(ctx); err != nil {
		return fmt.Errorf("subscribe redis pubsub: %w", err)
	}
	go func() {
		defer func() { _ = pubsub.Close() }()
		for {
			msg, err := pubsub.ReceiveMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				h.logger.Error("redis receive failed", "error", err)
				time.Sleep(time.Second)
				continue
			}
			subject := strings.TrimPrefix(msg.Channel, h.prefix)
			h.broadcast(subject, []byte(msg.Payload))
		}
	}()
	return nil
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	id, ok := gatewayctx.FromContext(r.Context())
	if !ok || id.Subject == "" {
		http.Error(w, "missing gateway identity", http.StatusUnauthorized)
		return
	}
	if _, err := uuid.Parse(id.Subject); err != nil {
		http.Error(w, "invalid subject", http.StatusUnauthorized)
		return
	}

	ch := make(chan []byte, 16)
	h.register(id.Subject, ch)
	defer h.unregister(id.Subject, ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case payload := <-ch:
			_, _ = fmt.Fprintf(w, "event: notification\ndata: %s\n\n", payload)
			flusher.Flush()
		case <-keepalive.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (h *Hub) register(subject string, ch chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[subject] == nil {
		h.clients[subject] = make(map[chan []byte]struct{})
	}
	h.clients[subject][ch] = struct{}{}
	if h.gauge != nil {
		h.gauge.Inc()
	}
}

func (h *Hub) unregister(subject string, ch chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[subject] != nil {
		delete(h.clients[subject], ch)
		if len(h.clients[subject]) == 0 {
			delete(h.clients, subject)
		}
	}
	close(ch)
	if h.gauge != nil {
		h.gauge.Dec()
	}
}

func (h *Hub) broadcast(subject string, payload []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients[subject] {
		select {
		case ch <- append([]byte(nil), payload...):
		default:
			h.logger.Warn("dropping sse event for slow client", "subject", subject)
		}
	}
}
