package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ledatu/csar-notify/internal/domain"
	"github.com/ledatu/csar-notify/internal/provider"
	"github.com/prometheus/client_golang/prometheus"
)

type storeAccessor interface {
	UpsertNotification(ctx context.Context, n *domain.Notification) error
	ListTopicSubscribers(ctx context.Context, topic string) ([]string, error)
	GetPreferences(ctx context.Context, subject string) ([]domain.Preference, error)
}

type Dispatcher struct {
	store          storeAccessor
	providers      *provider.Registry
	logger         *slog.Logger
	providerSends  *prometheus.CounterVec
	providerErrors *prometheus.CounterVec
}

func New(store storeAccessor, providers *provider.Registry, logger *slog.Logger, sends *prometheus.CounterVec, errs *prometheus.CounterVec) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Dispatcher{
		store:          store,
		providers:      providers,
		logger:         logger.With("component", "notify_dispatcher"),
		providerSends:  sends,
		providerErrors: errs,
	}
}

func (d *Dispatcher) Dispatch(ctx context.Context, n *domain.Notification) error {
	if err := domain.ValidateNotification(n); err != nil {
		return fmt.Errorf("dispatch validate notification: %w", err)
	}
	if err := d.store.UpsertNotification(ctx, n); err != nil {
		return fmt.Errorf("persist notification: %w", err)
	}

	recipients, err := d.resolveRecipients(ctx, n)
	if err != nil {
		return err
	}
	if len(recipients) == 0 {
		d.logger.Info("notification has no resolved recipients", "topic", n.Topic, "notification_id", n.ID)
		return nil
	}

	var sendErr error
	for _, recipient := range recipients {
		resolved, err := d.resolveChannels(ctx, recipient, n)
		if err != nil {
			sendErr = errors.Join(sendErr, fmt.Errorf("resolve channels for %s: %w", recipient, err))
			continue
		}
		for _, target := range resolved {
			prov, ok := d.providers.Get(target.Channel)
			if !ok {
				d.logger.Warn("provider missing for channel", "channel", target.Channel)
				continue
			}
			if d.providerSends != nil {
				d.providerSends.WithLabelValues(string(target.Channel)).Inc()
			}
			if err := prov.Send(ctx, n, recipient, target.Config); err != nil {
				if d.providerErrors != nil {
					d.providerErrors.WithLabelValues(string(target.Channel)).Inc()
				}
				sendErr = errors.Join(sendErr, fmt.Errorf("%s send to %s: %w", target.Channel, recipient, err))
			}
		}
	}
	return sendErr
}

func (d *Dispatcher) resolveRecipients(ctx context.Context, n *domain.Notification) ([]string, error) {
	if len(n.Recipients) > 0 {
		return append([]string(nil), n.Recipients...), nil
	}
	recipients, err := d.store.ListTopicSubscribers(ctx, n.Topic)
	if err != nil {
		return nil, fmt.Errorf("list topic subscribers: %w", err)
	}
	return recipients, nil
}

func (d *Dispatcher) resolveChannels(ctx context.Context, recipient string, n *domain.Notification) ([]domain.ResolvedPreference, error) {
	prefs, err := d.store.GetPreferences(ctx, recipient)
	if err != nil {
		return nil, err
	}
	byChannel := make(map[domain.Channel]domain.Preference, len(prefs))
	for _, pref := range prefs {
		byChannel[pref.Channel] = pref
	}

	if len(n.Channels) > 0 {
		out := make([]domain.ResolvedPreference, 0, len(n.Channels))
		for _, channel := range n.Channels {
			pref, ok := byChannel[channel]
			if ok && !pref.Enabled {
				continue
			}
			var cfg json.RawMessage
			if ok {
				cfg = pref.Config
			}
			out = append(out, domain.ResolvedPreference{Channel: channel, Config: cfg})
		}
		return out, nil
	}

	if len(prefs) == 0 {
		return []domain.ResolvedPreference{{Channel: domain.ChannelSite}}, nil
	}

	out := make([]domain.ResolvedPreference, 0, len(prefs))
	for _, pref := range prefs {
		if !pref.Enabled || !domain.TopicMatches(pref.Topics, n.Topic) {
			continue
		}
		out = append(out, domain.ResolvedPreference{Channel: pref.Channel, Config: pref.Config})
	}
	return out, nil
}
