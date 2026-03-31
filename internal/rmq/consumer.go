package rmq

import (
	"context"
	"fmt"
	"time"

	amqp091 "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type QueueConfig struct {
	Name     string
	Durable  bool
	Prefetch int
	Args     amqp091.Table
}

type ConsumerSession struct {
	ch         *amqp091.Channel
	deliveries <-chan amqp091.Delivery
}

func OpenConsumerSession(ctx context.Context, cm *ConnectionManager, cfg QueueConfig) (*ConsumerSession, error) {
	ch, err := cm.Channel()
	if err != nil {
		return nil, fmt.Errorf("open channel: %w", err)
	}

	args := cfg.Args
	if args == nil {
		args = amqp091.Table{}
	}

	_, err = ch.QueueDeclare(cfg.Name, cfg.Durable, false, false, false, args)
	if err != nil {
		_ = ch.Close()
		return nil, fmt.Errorf("declare queue %s: %w", cfg.Name, err)
	}

	prefetch := cfg.Prefetch
	if prefetch <= 0 {
		prefetch = 1
	}
	if err := ch.Qos(prefetch, 0, false); err != nil {
		_ = ch.Close()
		return nil, fmt.Errorf("set qos: %w", err)
	}

	deliveries, err := ch.ConsumeWithContext(ctx, cfg.Name, "", false, false, false, false, nil)
	if err != nil {
		_ = ch.Close()
		return nil, fmt.Errorf("consume queue %s: %w", cfg.Name, err)
	}

	return &ConsumerSession{ch: ch, deliveries: deliveries}, nil
}

func (s *ConsumerSession) Close() error {
	if s == nil || s.ch == nil {
		return nil
	}
	return s.ch.Close()
}

func (s *ConsumerSession) BatchConsume(ctx context.Context, maxN int, maxWait time.Duration) ([]amqp091.Delivery, error) {
	if s == nil {
		return nil, fmt.Errorf("rmq: nil consumer session")
	}
	return CollectBatch(ctx, s.deliveries, maxN, maxWait)
}

func CollectBatch(ctx context.Context, deliveries <-chan amqp091.Delivery, maxN int, maxWait time.Duration) ([]amqp091.Delivery, error) {
	if maxN < 1 {
		return nil, fmt.Errorf("rmq: maxN must be >= 1")
	}
	if maxWait < 0 {
		maxWait = 0
	}

	tracer := otel.Tracer("github.com/ledatu/csar-notify/internal/rmq")
	ctx, span := tracer.Start(ctx, "rmq.CollectBatch",
		oteltrace.WithSpanKind(oteltrace.SpanKindConsumer),
		oteltrace.WithAttributes(attribute.Int("rmq.max_batch", maxN)),
	)
	defer span.End()

	batch := make([]amqp091.Delivery, 0, maxN)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case d, ok := <-deliveries:
		if !ok {
			return nil, fmt.Errorf("rmq: deliveries channel closed")
		}
		ctx = otel.GetTextMapPropagator().Extract(ctx, extractTraceHeaders(d.Headers))
		batch = append(batch, d)
	}

	if maxN == 1 || maxWait == 0 {
		return batch, nil
	}

	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	for len(batch) < maxN {
		select {
		case <-ctx.Done():
			return batch, ctx.Err()
		case <-timer.C:
			return batch, nil
		case d, ok := <-deliveries:
			if !ok {
				return batch, fmt.Errorf("rmq: deliveries channel closed")
			}
			batch = append(batch, d)
		}
	}

	return batch, nil
}
