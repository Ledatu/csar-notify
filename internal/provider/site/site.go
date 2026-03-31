package site

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/ledatu/csar-notify/internal/config"
	"github.com/ledatu/csar-notify/internal/domain"
	"github.com/redis/go-redis/v9"
)

type deliveryWriter interface {
	InsertSiteDelivery(ctx context.Context, recipient string, n *domain.Notification) (*domain.Delivery, error)
}

type Provider struct {
	store         deliveryWriter
	redis         *redis.Client
	targets       map[string]config.SiteTargetConfig
	defaultTarget string
	logger        *slog.Logger
}

func New(store deliveryWriter, redisClient *redis.Client, siteCfg config.SiteProviderConfig, defaultPrefix string, logger *slog.Logger) (*Provider, error) {
	if logger == nil {
		logger = slog.Default()
	}
	targets, defaultTarget, err := siteCfg.EffectiveTargets(defaultPrefix)
	if err != nil {
		return nil, err
	}
	return &Provider{
		store:         store,
		redis:         redisClient,
		targets:       targets,
		defaultTarget: defaultTarget,
		logger:        logger.With("component", "notify_provider_site"),
	}, nil
}

func (p *Provider) Name() string {
	return "site"
}

func (p *Provider) Channel() domain.Channel {
	return domain.ChannelSite
}

func (p *Provider) Send(ctx context.Context, n *domain.Notification, recipientUUID string, prefConfig json.RawMessage) error {
	targetName, err := p.resolveTarget(n, prefConfig)
	if err != nil {
		return err
	}
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
	target := p.targets[targetName]
	if err := p.redis.Publish(ctx, target.RedisPrefix+recipientUUID, payload).Err(); err != nil {
		return fmt.Errorf("publish site event: %w", err)
	}
	return nil
}

func (p *Provider) Close() error {
	return nil
}

func (p *Provider) Targets() map[string]config.SiteTargetConfig {
	out := make(map[string]config.SiteTargetConfig, len(p.targets))
	for name, target := range p.targets {
		out[name] = target
	}
	return out
}

func (p *Provider) DefaultTarget() string {
	return p.defaultTarget
}

func (p *Provider) resolveTarget(n *domain.Notification, prefConfig json.RawMessage) (string, error) {
	target := p.defaultTarget

	if len(prefConfig) > 0 {
		var pref struct {
			Target string `json:"target"`
		}
		if err := json.Unmarshal(prefConfig, &pref); err != nil {
			return "", fmt.Errorf("decode site preference: %w", err)
		}
		if value := strings.TrimSpace(pref.Target); value != "" {
			target = value
		}
	}

	if n != nil && n.Metadata != nil {
		if value := strings.TrimSpace(n.Metadata["site_target"]); value != "" {
			target = value
		}
	}

	if _, ok := p.targets[target]; !ok {
		return "", fmt.Errorf("site target %q is not configured", target)
	}
	return target, nil
}
