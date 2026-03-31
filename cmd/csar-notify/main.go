package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ledatu/csar-core/configload"
	"github.com/ledatu/csar-core/gatewayctx"
	"github.com/ledatu/csar-core/health"
	"github.com/ledatu/csar-core/httpmiddleware"
	"github.com/ledatu/csar-core/httpserver"
	"github.com/ledatu/csar-core/logutil"
	"github.com/ledatu/csar-core/observe"
	"github.com/ledatu/csar-core/pgutil"
	"github.com/ledatu/csar-core/tlsx"
	"github.com/ledatu/csar-notify/internal/config"
	"github.com/ledatu/csar-notify/internal/consumer"
	"github.com/ledatu/csar-notify/internal/dispatch"
	"github.com/ledatu/csar-notify/internal/inbox"
	"github.com/ledatu/csar-notify/internal/ingest"
	notifymetrics "github.com/ledatu/csar-notify/internal/metrics"
	"github.com/ledatu/csar-notify/internal/pipeline"
	"github.com/ledatu/csar-notify/internal/preferences"
	"github.com/ledatu/csar-notify/internal/provider"
	siteprovider "github.com/ledatu/csar-notify/internal/provider/site"
	telegramprovider "github.com/ledatu/csar-notify/internal/provider/telegram"
	webpushprovider "github.com/ledatu/csar-notify/internal/provider/webpush"
	"github.com/ledatu/csar-notify/internal/pushsubscription"
	"github.com/ledatu/csar-notify/internal/rmq"
	"github.com/ledatu/csar-notify/internal/sse"
	"github.com/ledatu/csar-notify/internal/store"
	"github.com/ledatu/csar-notify/internal/vapid"
	"github.com/redis/go-redis/v9"
)

var Version = "dev"

func main() {
	inner := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(logutil.NewRedactingHandler(inner))

	sf := configload.NewSourceFlags()
	sf.RegisterFlags(flag.CommandLine)

	otlpEndpoint := ""
	otlpInsecure := false
	flag.StringVar(&otlpEndpoint, "otlp-endpoint", otlpEndpoint, "OTLP gRPC endpoint (empty disables tracing)")
	flag.BoolVar(&otlpInsecure, "otlp-insecure", otlpInsecure, "insecure OTLP connection")
	flag.Parse()

	if err := run(sf, otlpEndpoint, otlpInsecure, logger); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(sf *configload.SourceFlags, otlpEndpoint string, otlpInsecure bool, logger *slog.Logger) error {
	ctx := context.Background()

	srcParams := sf.SourceParams()
	cfg, err := configload.LoadInitial(ctx, &srcParams, logger, config.LoadFromBytes)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ep := otlpEndpoint
	if ep == "" {
		ep = cfg.Tracing.Endpoint
	}
	tp, err := observe.InitTracer(ctx, observe.TraceConfig{
		ServiceName:    cfg.Service.Name,
		ServiceVersion: Version,
		Endpoint:       ep,
		SampleRate:     cfg.Tracing.SampleRatio,
		Insecure:       otlpInsecure,
	})
	if err != nil {
		return fmt.Errorf("init tracer: %w", err)
	}
	defer func() { _ = tp.Close() }()

	reg := observe.NewRegistry()

	pool, err := pgutil.NewPool(ctx, cfg.Database.DSN, pgutil.WithLogger(logger.With("component", "postgres")))
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()

	pgStore := store.NewPostgres(pool, logger.With("component", "notify_store"))
	if err := pgStore.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	redisOpts, err := redis.ParseURL(cfg.Redis.URL)
	if err != nil {
		return fmt.Errorf("parse redis url: %w", err)
	}
	redisClient := redis.NewClient(redisOpts)
	defer func() { _ = redisClient.Close() }()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping: %w", err)
	}

	cm := rmq.NewConnectionManager(rmq.ConnectionConfig{
		URL:            cfg.RabbitMQ.URL,
		ReconnectDelay: cfg.RabbitMQ.ReconnectDelay.Std(),
	}, logger.With("component", "rabbitmq"))
	if err := cm.Connect(ctx); err != nil {
		return fmt.Errorf("rabbitmq connect: %w", err)
	}
	defer func() { _ = cm.Close() }()

	if err := declareDLQ(cm, cfg.Consumer.DLQ.Name, cfg.Consumer.DLQ.Durable); err != nil {
		return fmt.Errorf("declare rabbitmq dlq: %w", err)
	}

	pub := rmq.NewPublisher(cm, cfg.Consumer.Queue.Name)
	var bufDepth func() float64
	m := notifymetrics.New(reg, func() float64 {
		if bufDepth != nil {
			return bufDepth()
		}
		return 0
	})

	buf := pipeline.NewBuffer(cfg.Ingest.BufferSize, cfg.Ingest.PublisherWorkers, pub, logger, &pipeline.BufferMetrics{
		EventsPublished: m.EventsPublished,
		EventsDropped:   m.EventsDropped,
	})
	defer buf.Close()
	bufDepth = func() float64 { return float64(buf.Depth()) }

	siteTargets, siteDefaultTarget, err := cfg.Providers.Site.EffectiveTargets(cfg.Redis.Prefix)
	if err != nil {
		return fmt.Errorf("resolve site provider targets: %w", err)
	}
	hubPrefixes := make(map[string]string, len(siteTargets))
	for name, target := range siteTargets {
		hubPrefixes[name] = target.RedisPrefix
	}

	hub := sse.New(redisClient, hubPrefixes, siteDefaultTarget, logger, m.SSEConnections)
	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()
	if err := hub.Start(appCtx); err != nil {
		return fmt.Errorf("start sse hub: %w", err)
	}

	providers, err := buildProviders(cfg, pgStore, redisClient, logger)
	if err != nil {
		return fmt.Errorf("build providers: %w", err)
	}
	defer func() { _ = providers.Close() }()

	dispatcher := dispatch.New(pgStore, providers, logger, m.ProviderSends, m.ProviderErrors)
	go consumer.Run(appCtx, cm, dispatcher, cfg, logger, &consumer.ConsumerMetrics{
		BatchSize:         m.BatchSize,
		BatchFlushSeconds: m.BatchFlushSeconds,
		NotificationsSeen: m.NotificationsSeen,
		ConsumerErrors:    m.ConsumerErrors,
	})

	mux := http.NewServeMux()
	mux.Handle("POST /ingest", ingest.NewHTTPHandler(buf, logger, m.IngestTotal, m.IngestDuration))
	inbox.New(pgStore).Register(mux)
	preferences.New(pgStore).Register(mux)
	vapid.New(cfg.Providers.WebPush.VAPIDPublicKey).Register(mux)
	pushsubscription.New(pgStore).Register(mux)
	mux.Handle("GET /notifications/stream", http.HandlerFunc(hub.ServeHTTP))

	httpStack := httpmiddleware.Chain(
		httpmiddleware.RequestID,
		httpmiddleware.AccessLog(logger.With("component", "http")),
		httpmiddleware.Recover(logger),
		httpmiddleware.MaxBodySize(1<<20),
		gatewayctx.TrustedMiddleware(buildTrustFunc(cfg)),
	)

	var httpTLS *tlsx.ServerConfig
	if cfg.TLS.CertFile != "" || cfg.TLS.KeyFile != "" {
		tlsCfg := cfg.TLS.ToTLSx()
		httpTLS = &tlsCfg
	}

	httpSrv, err := httpserver.New(&httpserver.Config{
		Addr:         cfg.HTTPAddr(),
		Handler:      httpStack(mux),
		TLS:          httpTLS,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0,
	}, logger.With("component", "http_server"))
	if err != nil {
		return fmt.Errorf("http server: %w", err)
	}

	rc := health.NewReadinessChecker(Version, true)
	rc.Register("rabbitmq", func() health.CheckStatus {
		if cm.IsConnected() {
			return health.CheckStatus{Status: "ok"}
		}
		return health.CheckStatus{Status: "fail", Detail: "not connected"}
	})
	rc.Register("postgres", func() health.CheckStatus {
		cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(cctx); err != nil {
			return health.CheckStatus{Status: "fail", Detail: err.Error()}
		}
		return health.CheckStatus{Status: "ok"}
	})
	rc.Register("redis", func() health.CheckStatus {
		cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := redisClient.Ping(cctx).Err(); err != nil {
			return health.CheckStatus{Status: "fail", Detail: err.Error()}
		}
		return health.CheckStatus{Status: "ok"}
	})
	rc.Register("notify_buffer", func() health.CheckStatus {
		capacity := buf.Capacity()
		if capacity == 0 {
			return health.CheckStatus{Status: "ok"}
		}
		depth := buf.Depth()
		if float64(depth)/float64(capacity) >= 0.8 {
			return health.CheckStatus{Status: "degraded", Detail: "buffer over 80% full"}
		}
		return health.CheckStatus{Status: "ok"}
	})

	healthSidecar, err := health.NewSidecar(health.SidecarConfig{
		Addr:      cfg.HealthAddr(),
		Version:   Version,
		Readiness: rc,
		Metrics:   observe.MetricsHandler(reg),
		Logger:    logger.With("component", "health"),
	})
	if err != nil {
		return fmt.Errorf("health sidecar: %w", err)
	}
	go func() {
		if err := healthSidecar.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("health sidecar error", "error", err)
		}
	}()

	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server error", "error", err)
		}
	}()

	logger.Info("csar-notify started", "http", cfg.HTTPAddr(), "health", cfg.HealthAddr())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	logger.Info("shutting down", "signal", fmt.Sprintf("%v", sig))

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	appCancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown", "error", err)
	}
	if err := healthSidecar.Shutdown(shutdownCtx); err != nil {
		logger.Error("health sidecar shutdown", "error", err)
	}
	return nil
}

func buildProviders(cfg *config.Config, pgStore *store.Postgres, redisClient *redis.Client, logger *slog.Logger) (*provider.Registry, error) {
	items := make([]provider.Provider, 0, 4)
	if cfg.Providers.Site.Enabled {
		prov, err := siteprovider.New(pgStore, redisClient, cfg.Providers.Site, cfg.Redis.Prefix, logger)
		if err != nil {
			return nil, fmt.Errorf("site provider: %w", err)
		}
		items = append(items, prov)
	}
	if cfg.Providers.Telegram.Enabled {
		prov, err := telegramprovider.New(nil, cfg.Providers.Telegram, logger)
		if err != nil {
			return nil, fmt.Errorf("telegram provider: %w", err)
		}
		items = append(items, prov)
	}
	if cfg.Providers.WebPush.Enabled {
		items = append(items, webpushprovider.New(pgStore, cfg.Providers.WebPush, logger))
	}
	return provider.NewRegistry(items...), nil
}

func buildTrustFunc(cfg *config.Config) gatewayctx.TrustFunc {
	cnm := cfg.HTTP.AllowedClientCN
	if cnm == "" {
		return func(*http.Request) error { return nil }
	}
	return func(r *http.Request) error {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			return fmt.Errorf("client certificate required")
		}
		if peer := r.TLS.PeerCertificates[0]; peer.Subject.CommonName != cnm {
			return fmt.Errorf("client CN %q not authorized", peer.Subject.CommonName)
		}
		return nil
	}
}

func declareDLQ(cm *rmq.ConnectionManager, dlq string, durable bool) error {
	ch, err := cm.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	if _, err := ch.QueueDeclare(dlq, durable, false, false, false, nil); err != nil {
		return fmt.Errorf("declare dlq: %w", err)
	}
	return nil
}
