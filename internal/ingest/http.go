package ingest

import (
	"log/slog"
	"net/http"
	"time"

	csarerrors "github.com/ledatu/csar-core/errors"
	"github.com/ledatu/csar-core/httpx"
	"github.com/ledatu/csar-notify/internal/domain"
	"github.com/prometheus/client_golang/prometheus"
)

type submitter interface {
	Submit(*domain.Notification) error
}

type httpIngestBody struct {
	Notifications []*domain.Notification `json:"notifications"`
}

type HTTPHandler struct {
	buf            submitter
	logger         *slog.Logger
	ingestTotal    *prometheus.CounterVec
	ingestDuration *prometheus.HistogramVec
}

func NewHTTPHandler(buf submitter, logger *slog.Logger, ingestTotal *prometheus.CounterVec, ingestDuration *prometheus.HistogramVec) *HTTPHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &HTTPHandler{
		buf:            buf,
		logger:         logger.With("component", "notify_ingest_http"),
		ingestTotal:    ingestTotal,
		ingestDuration: ingestDuration,
	}
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		if h.ingestDuration != nil {
			h.ingestDuration.WithLabelValues("http").Observe(time.Since(start).Seconds())
		}
	}()

	var body httpIngestBody
	if err := httpx.ReadJSON(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if len(body.Notifications) == 0 {
		httpx.WriteError(w, csarerrors.Validation("notifications required"))
		return
	}

	for _, notification := range body.Notifications {
		if err := domain.ValidateNotification(notification); err != nil {
			httpx.WriteError(w, csarerrors.Validation("%v", err))
			return
		}
	}

	for idx, notification := range body.Notifications {
		if err := h.buf.Submit(notification); err != nil {
			h.logger.Warn("notify ingest buffer full", "topic", notification.Topic, "accepted", idx, "total", len(body.Notifications))
			httpx.WriteError(w, csarerrors.Unavailable("notify ingest buffer full after %d of %d notifications", idx, len(body.Notifications)))
			return
		}
	}

	if h.ingestTotal != nil {
		h.ingestTotal.WithLabelValues("http").Add(float64(len(body.Notifications)))
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{
		"status":   "accepted",
		"accepted": len(body.Notifications),
	})
}
