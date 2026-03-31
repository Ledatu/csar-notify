package metrics

import "github.com/prometheus/client_golang/prometheus"

type Metrics struct {
	IngestTotal       *prometheus.CounterVec
	IngestDuration    *prometheus.HistogramVec
	BufferDepth       prometheus.GaugeFunc
	EventsPublished   prometheus.Counter
	EventsDropped     *prometheus.CounterVec
	BatchSize         prometheus.Histogram
	BatchFlushSeconds prometheus.Histogram
	NotificationsSeen prometheus.Counter
	ConsumerErrors    *prometheus.CounterVec
	ProviderSends     *prometheus.CounterVec
	ProviderErrors    *prometheus.CounterVec
	SSEConnections    prometheus.Gauge
}

type DepthFunc func() float64

func New(reg *prometheus.Registry, depthFn DepthFunc) *Metrics {
	m := &Metrics{
		IngestTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "notify",
			Name:      "ingest_total",
			Help:      "Total notifications received by ingest handlers.",
		}, []string{"transport"}),

		IngestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "notify",
			Name:      "ingest_duration_seconds",
			Help:      "Ingest handler latency.",
			Buckets:   []float64{.0005, .001, .005, .01, .05, .1},
		}, []string{"transport"}),

		BufferDepth: prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Namespace: "notify",
			Name:      "buffer_depth",
			Help:      "Number of notifications in the in-memory pipeline buffer.",
		}, depthFn),

		EventsPublished: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "notify",
			Name:      "events_published_total",
			Help:      "Notifications successfully published to RabbitMQ.",
		}),

		EventsDropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "notify",
			Name:      "events_dropped_total",
			Help:      "Notifications dropped before dispatch.",
		}, []string{"reason"}),

		BatchSize: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "notify",
			Name:      "batch_size",
			Help:      "Number of notifications per consumer batch.",
			Buckets:   []float64{1, 5, 10, 25, 50, 100, 200, 500},
		}),

		BatchFlushSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "notify",
			Name:      "batch_flush_duration_seconds",
			Help:      "Time spent dispatching a consumer batch.",
			Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 5},
		}),

		NotificationsSeen: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "notify",
			Name:      "notifications_seen_total",
			Help:      "Notifications processed by the consumer.",
		}),

		ConsumerErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "notify",
			Name:      "consumer_errors_total",
			Help:      "Consumer-side errors by type.",
		}, []string{"type"}),

		ProviderSends: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "notify",
			Name:      "provider_sends_total",
			Help:      "Provider send attempts by channel.",
		}, []string{"channel"}),

		ProviderErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "notify",
			Name:      "provider_errors_total",
			Help:      "Provider send errors by channel.",
		}, []string{"channel"}),

		SSEConnections: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "notify",
			Name:      "sse_connections",
			Help:      "Active notification SSE connections.",
		}),
	}

	reg.MustRegister(
		m.IngestTotal,
		m.IngestDuration,
		m.BufferDepth,
		m.EventsPublished,
		m.EventsDropped,
		m.BatchSize,
		m.BatchFlushSeconds,
		m.NotificationsSeen,
		m.ConsumerErrors,
		m.ProviderSends,
		m.ProviderErrors,
		m.SSEConnections,
	)

	return m
}
