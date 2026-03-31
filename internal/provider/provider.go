package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ledatu/csar-notify/internal/domain"
)

type Provider interface {
	Name() string
	Channel() domain.Channel
	Send(ctx context.Context, n *domain.Notification, recipientUUID string, prefConfig json.RawMessage) error
	Close() error
}

type Registry struct {
	byChannel map[domain.Channel]Provider
}

func NewRegistry(providers ...Provider) *Registry {
	r := &Registry{byChannel: make(map[domain.Channel]Provider, len(providers))}
	for _, p := range providers {
		if p == nil {
			continue
		}
		r.byChannel[p.Channel()] = p
	}
	return r
}

func (r *Registry) Get(channel domain.Channel) (Provider, bool) {
	if r == nil {
		return nil, false
	}
	p, ok := r.byChannel[channel]
	return p, ok
}

func (r *Registry) Close() error {
	if r == nil {
		return nil
	}
	var firstErr error
	for _, p := range r.byChannel {
		if err := p.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close provider %s: %w", p.Name(), err)
		}
	}
	return firstErr
}
