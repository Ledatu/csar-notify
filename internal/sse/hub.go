package sse

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ledatu/csar-core/gatewayctx"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

type Hub struct {
	redis          *redis.Client
	targetPrefixes map[string]string
	defaultTarget  string
	logger         *slog.Logger
	gauge          prometheus.Gauge

	mu      sync.RWMutex
	clients map[string]map[chan []byte]struct{}
}

func New(redisClient *redis.Client, targetPrefixes map[string]string, defaultTarget string, logger *slog.Logger, gauge prometheus.Gauge) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	if len(targetPrefixes) == 0 {
		targetPrefixes = map[string]string{
			"default": "notify:",
		}
	}
	if defaultTarget == "" {
		defaultTarget = "default"
	}
	return &Hub{
		redis:          redisClient,
		targetPrefixes: clonePrefixes(targetPrefixes),
		defaultTarget:  defaultTarget,
		logger:         logger.With("component", "notify_sse"),
		gauge:          gauge,
		clients:        make(map[string]map[chan []byte]struct{}),
	}
}

func (h *Hub) Start(ctx context.Context) error {
	if h.redis == nil {
		return nil
	}
	patterns := make([]string, 0, len(h.targetPrefixes))
	for _, prefix := range h.targetPrefixes {
		patterns = append(patterns, prefix+"*")
	}
	sort.Strings(patterns)
	pubsub := h.redis.PSubscribe(ctx, patterns...)
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
			target, subject, ok := h.resolveChannel(msg.Channel)
			if !ok {
				h.logger.Warn("dropping redis message for unknown target", "channel", msg.Channel)
				continue
			}
			h.broadcast(h.clientKey(target, subject), []byte(msg.Payload))
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
	target := strings.TrimSpace(r.URL.Query().Get("target"))
	if target == "" {
		target = h.defaultTarget
	}
	if _, ok := h.targetPrefixes[target]; !ok {
		http.Error(w, "unknown notification target", http.StatusBadRequest)
		return
	}

	ch := make(chan []byte, 16)
	key := h.clientKey(target, id.Subject)
	h.register(key, ch)
	defer h.unregister(key, ch)

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

func (h *Hub) clientKey(target string, subject string) string {
	return target + "\x00" + subject
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

func (h *Hub) resolveChannel(channel string) (string, string, bool) {
	longestPrefix := ""
	target := ""
	for name, prefix := range h.targetPrefixes {
		if strings.HasPrefix(channel, prefix) && len(prefix) > len(longestPrefix) {
			longestPrefix = prefix
			target = name
		}
	}
	if longestPrefix == "" {
		return "", "", false
	}
	return target, strings.TrimPrefix(channel, longestPrefix), true
}

func clonePrefixes(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for name, prefix := range in {
		out[name] = prefix
	}
	return out
}
