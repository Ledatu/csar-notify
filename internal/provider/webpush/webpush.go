package webpush

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/ledatu/csar-notify/internal/config"
	"github.com/ledatu/csar-notify/internal/domain"
	"github.com/ledatu/csar-notify/internal/provider"
	"github.com/ledatu/csar-notify/internal/store"
)

type subscriptionStore interface {
	ListPushSubscriptionsForSubject(ctx context.Context, subject string) ([]store.PushSubscriptionRow, error)
	DeletePushSubscription(ctx context.Context, subject, endpoint string) error
}

// Provider sends encrypted Web Push notifications to registered browser endpoints.
type Provider struct {
	store      subscriptionStore
	cfg        config.WebPushProviderConfig
	logger     *slog.Logger
	httpClient webpush.HTTPClient
}

func New(s subscriptionStore, cfg config.WebPushProviderConfig, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{
		store:      s,
		cfg:        cfg,
		logger:     logger.With("component", "notify_provider_webpush"),
		httpClient: newAuthHTTPClient(nil, cfg.Subscriber, cfg.VAPIDPublicKey, cfg.VAPIDPrivateKey),
	}
}

func (p *Provider) Name() string {
	return "web_push"
}

func (p *Provider) Channel() domain.Channel {
	return domain.ChannelWebPush
}

func (p *Provider) Send(ctx context.Context, n *domain.Notification, recipientUUID string, _ json.RawMessage) error {
	subs, err := p.store.ListPushSubscriptionsForSubject(ctx, recipientUUID)
	if err != nil {
		return fmt.Errorf("list push subscriptions: %w", err)
	}
	if len(subs) == 0 {
		return nil
	}

	tag := "support-notify"
	if n.ID != "" {
		tag = "notify-" + n.ID
	}
	payload, err := json.Marshal(struct {
		Title    string            `json:"title"`
		Body     string            `json:"body"`
		Link     string            `json:"link"`
		Tag      string            `json:"tag"`
		Metadata map[string]string `json:"metadata,omitempty"`
	}{
		Title:    n.Title,
		Body:     n.Body,
		Link:     n.Link,
		Tag:      tag,
		Metadata: n.Metadata,
	})
	if err != nil {
		return fmt.Errorf("marshal web push payload: %w", err)
	}

	opts := &webpush.Options{
		Subscriber:      strings.TrimSpace(p.cfg.Subscriber),
		TTL:             86400,
		VAPIDPublicKey:  strings.TrimSpace(p.cfg.VAPIDPublicKey),
		VAPIDPrivateKey: strings.TrimSpace(p.cfg.VAPIDPrivateKey),
		HTTPClient:      p.httpClient,
	}

	var sendErr error
	for _, sub := range subs {
		s := &webpush.Subscription{
			Endpoint: sub.Endpoint,
			Keys: webpush.Keys{
				Auth:   sub.AuthKey,
				P256dh: sub.P256dhKey,
			},
		}
		resp, err := webpush.SendNotificationWithContext(ctx, payload, s, opts)
		if err != nil {
			sendErr = errors.Join(sendErr, provider.TemporarySendError(domain.ChannelWebPush, err))
			p.logger.Warn("web push send failed", "endpoint", truncateEndpoint(sub.Endpoint), "error", err)
			continue
		}
		if resp != nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			_ = resp.Body.Close()
			bodyText := strings.TrimSpace(string(body))
			if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
				if delErr := p.store.DeletePushSubscription(ctx, recipientUUID, sub.Endpoint); delErr != nil {
					p.logger.Warn("drop expired push subscription", "endpoint", truncateEndpoint(sub.Endpoint), "error", delErr)
					sendErr = errors.Join(sendErr, provider.TemporarySendError(domain.ChannelWebPush, delErr))
				}
				continue
			}
			if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
				statusErr := fmt.Errorf("web push unexpected status %d", resp.StatusCode)
				if bodyText != "" {
					statusErr = fmt.Errorf("%w: %s", statusErr, bodyText)
				}
				if isPermanentWebPushFailure(resp.StatusCode, bodyText) {
					sendErr = errors.Join(sendErr, provider.PermanentSendError(domain.ChannelWebPush, statusErr))
				} else {
					sendErr = errors.Join(sendErr, provider.TemporarySendError(domain.ChannelWebPush, statusErr))
				}
				p.logger.Warn(
					"web push unexpected status",
					"status", resp.StatusCode,
					"endpoint", truncateEndpoint(sub.Endpoint),
					"body", bodyText,
				)
			}
		}
	}
	return sendErr
}

func truncateEndpoint(ep string) string {
	const max = 64
	if len(ep) <= max {
		return ep
	}
	return ep[:max] + "…"
}

func isPermanentWebPushFailure(statusCode int, body string) bool {
	reason := webPushFailureReason(body)
	if statusCode == http.StatusTooManyRequests && reason == "TooManyProviderTokenUpdates" {
		return true
	}
	switch statusCode {
	case http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusGone:
		return true
	default:
		return false
	}
}

func webPushFailureReason(body string) string {
	var payload struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Reason)
}

func (p *Provider) Close() error {
	return nil
}
