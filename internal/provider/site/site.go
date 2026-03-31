package site

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/ledatu/csar-notify/internal/domain"
	"github.com/redis/go-redis/v9"
)

type deliveryWriter interface {
	InsertSiteDelivery(ctx context.Context, recipient string, n *domain.Notification) (*domain.Delivery, error)
}

type Provider struct {
	store  deliveryWriter
	redis  *redis.Client
	prefix string
	logger *slog.Logger
}

func New(store deliveryWriter, redisClient *redis.Client, prefix string, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{
		store:  store,
		redis:  redisClient,
		prefix: prefix,
		logger: logger.With("component", "notify_provider_site"),
	}
}

func (p *Provider) Name() string {
	return "site"
}

func (p *Provider) Channel() domain.Channel {
	return domain.ChannelSite
}

func (p *Provider) Send(ctx context.Context, n *domain.Notification, recipientUUID string, _ json.RawMessage) error {
	item, err := p.store.InsertSiteDelivery(ctx, recipientUUID, n)
	if err != nil {
		return fmt.Errorf("site delivery insert: %w", err)
	}
	if p.redis == nil {
		return nil
	}

	payload, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("marshal site event: %w", err)
	}
	if err := p.redis.Publish(ctx, p.prefix+recipientUUID, payload).Err(); err != nil {
		return fmt.Errorf("publish site event: %w", err)
	}
	return nil
}

func (p *Provider) Close() error {
	return nil
}
