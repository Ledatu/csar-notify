package site

import (
	"encoding/json"
	"testing"

	"github.com/ledatu/csar-notify/internal/config"
	"github.com/ledatu/csar-notify/internal/domain"
)

func TestResolveTargetUsesPreferenceThenMetadata(t *testing.T) {
	t.Parallel()

	provider := &Provider{
		targets: map[string]config.SiteTargetConfig{
			"admin": {RedisPrefix: "notify:"},
			"main":  {RedisPrefix: "notify:main:"},
		},
		defaultTarget: "admin",
	}

	target, err := provider.resolveTarget(&domain.Notification{
		Metadata: map[string]string{"site_target": "main"},
	}, json.RawMessage(`{"target":"admin"}`))
	if err != nil {
		t.Fatalf("resolveTarget() error = %v", err)
	}
	if target != "main" {
		t.Fatalf("target = %q, want metadata override", target)
	}
}

func TestResolveTargetRejectsUnknownTarget(t *testing.T) {
	t.Parallel()

	provider := &Provider{
		targets: map[string]config.SiteTargetConfig{
			"admin": {RedisPrefix: "notify:"},
		},
		defaultTarget: "admin",
	}

	_, err := provider.resolveTarget(nil, json.RawMessage(`{"target":"main"}`))
	if err == nil {
		t.Fatal("resolveTarget() error = nil, want error")
	}
}
