package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/ledatu/csar-notify/internal/config"
	"github.com/ledatu/csar-notify/internal/dispatch"
	"github.com/ledatu/csar-notify/internal/domain"
	"github.com/ledatu/csar-notify/internal/rmq"
	"github.com/prometheus/client_golang/prometheus"
	amqp091 "github.com/rabbitmq/amqp091-go"
)

type ConsumerMetrics struct {
	BatchSize         prometheus.Histogram
	BatchFlushSeconds prometheus.Histogram
	NotificationsSeen prometheus.Counter
	ConsumerErrors    *prometheus.CounterVec
}

func Run(ctx context.Context, cm *rmq.ConnectionManager, dispatcher *dispatch.Dispatcher, cfg *config.Config, logger *slog.Logger, m *ConsumerMetrics) {
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("component", "notify_consumer")
	ccfg := &cfg.Consumer

	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if err := runSession(ctx, cm, dispatcher, ccfg, logger, m); err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return
			}
			logger.Error("consumer session ended", "error", err, "retry_in", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
	}
}

func runSession(ctx context.Context, cm *rmq.ConnectionManager, dispatcher *dispatch.Dispatcher, ccfg *config.ConsumerConfig, logger *slog.Logger, m *ConsumerMetrics) error {
	queueCfg := rmq.QueueConfig{
		Name:     ccfg.Queue.Name,
		Durable:  ccfg.Queue.Durable,
		Prefetch: ccfg.Queue.Prefetch,
		Args: amqp091.Table{
			"x-dead-letter-exchange":    "",
			"x-dead-letter-routing-key": ccfg.DLQ.Name,
		},
	}

	sess, err := rmq.OpenConsumerSession(ctx, cm, queueCfg)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()

	flush := ccfg.FlushInterval.Std()
	batchSize := ccfg.BatchSize
	maxRedeliver := ccfg.MaxRedeliveries

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		deliveries, err := sess.BatchConsume(ctx, batchSize, flush)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			return err
		}
		if len(deliveries) == 0 {
			continue
		}

		notifications, good, poison := decodeBatch(deliveries, maxRedeliver, logger, m)
		for i := range poison {
			_ = poison[i].Nack(false, false)
		}
		if len(notifications) == 0 {
			continue
		}

		if m != nil {
			m.BatchSize.Observe(float64(len(notifications)))
			m.NotificationsSeen.Add(float64(len(notifications)))
		}

		start := time.Now()
		dispatchCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		err = dispatchBatch(dispatchCtx, dispatcher, notifications)
		cancel()
		if m != nil {
			m.BatchFlushSeconds.Observe(time.Since(start).Seconds())
		}
		if err != nil {
			logger.Error("dispatch batch failed", "error", err, "batch_len", len(notifications))
			if m != nil {
				m.ConsumerErrors.WithLabelValues("dispatch_error").Inc()
			}
			for i := range good {
				_ = good[i].Nack(false, true)
			}
			continue
		}

		if err := good[len(good)-1].Ack(true); err != nil {
			logger.Error("batch ack failed", "error", err, "batch_len", len(good))
		}
	}
}

func dispatchBatch(ctx context.Context, dispatcher *dispatch.Dispatcher, notifications []domain.Notification) error {
	for i := range notifications {
		if err := dispatcher.Dispatch(ctx, &notifications[i]); err != nil {
			return err
		}
	}
	return nil
}

func decodeBatch(batch []amqp091.Delivery, maxRedeliver int, logger *slog.Logger, m *ConsumerMetrics) ([]domain.Notification, []amqp091.Delivery, []amqp091.Delivery) {
	notifications := make([]domain.Notification, 0, len(batch))
	good := make([]amqp091.Delivery, 0, len(batch))
	var poison []amqp091.Delivery

	for i := range batch {
		if maxRedeliver > 0 && deathCount(&batch[i]) >= maxRedeliver {
			logger.Warn("message exceeded max redeliveries, sending to DLQ", "delivery_tag", batch[i].DeliveryTag, "deaths", deathCount(&batch[i]))
			if m != nil {
				m.ConsumerErrors.WithLabelValues("dlq").Inc()
			}
			poison = append(poison, batch[i])
			continue
		}

		var n domain.Notification
		if err := json.Unmarshal(batch[i].Body, &n); err != nil {
			logger.Warn("notify message decode failed, sending to DLQ", "error", err)
			if m != nil {
				m.ConsumerErrors.WithLabelValues("decode_error").Inc()
			}
			poison = append(poison, batch[i])
			continue
		}
		notifications = append(notifications, n)
		good = append(good, batch[i])
	}

	return notifications, good, poison
}

func deathCount(d *amqp091.Delivery) int {
	xDeath, ok := d.Headers["x-death"]
	if !ok {
		if d.Redelivered {
			return 1
		}
		return 0
	}
	deaths, ok := xDeath.([]interface{})
	if !ok || len(deaths) == 0 {
		return 0
	}
	first, ok := deaths[0].(amqp091.Table)
	if !ok {
		return 0
	}
	count, ok := first["count"].(int64)
	if !ok {
		return 0
	}
	return int(count)
}
