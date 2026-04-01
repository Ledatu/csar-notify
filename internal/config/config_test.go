package config

import "testing"

func TestLoadFromBytesSupportsNamedTargetsAndBots(t *testing.T) {
	t.Parallel()

	cfg, err := LoadFromBytes([]byte(`
database:
  dsn: "postgres://notify"
rabbitmq:
  url: "amqp://guest:guest@localhost:5672/"
redis:
  url: "redis://localhost:6379/0"
providers:
  site:
    enabled: true
    default_target: admin
    targets:
      admin: {}
      main:
        redis_prefix: "notify:main:"
  telegram:
    enabled: true
    default_bot: support
    bots:
      support:
        token: support-token
      main-site:
        token: main-token
`))
	if err != nil {
		t.Fatalf("LoadFromBytes() error = %v", err)
	}

	targets, defaultTarget, err := cfg.Providers.Site.EffectiveTargets(cfg.Redis.Prefix)
	if err != nil {
		t.Fatalf("EffectiveTargets() error = %v", err)
	}
	if defaultTarget != "admin" {
		t.Fatalf("default target = %q, want admin", defaultTarget)
	}
	if got := targets["admin"].RedisPrefix; got != "notify:admin:" {
		t.Fatalf("admin redis prefix = %q, want notify:admin:", got)
	}
	if got := targets["main"].RedisPrefix; got != "notify:main:" {
		t.Fatalf("main redis prefix = %q, want notify:main:", got)
	}

	bots, defaultBot, err := cfg.Providers.Telegram.EffectiveBots()
	if err != nil {
		t.Fatalf("EffectiveBots() error = %v", err)
	}
	if defaultBot != "support" {
		t.Fatalf("default bot = %q, want support", defaultBot)
	}
	if got := bots["support"].APIBaseURL; got != "https://api.telegram.org" {
		t.Fatalf("support APIBaseURL = %q, want telegram default", got)
	}
}

func TestLoadFromBytesRejectsUnsupportedEmailProvider(t *testing.T) {
	t.Parallel()

	_, err := LoadFromBytes([]byte(`
database:
  dsn: "postgres://notify"
rabbitmq:
  url: "amqp://guest:guest@localhost:5672/"
redis:
  url: "redis://localhost:6379/0"
providers:
  email:
    enabled: true
`))
	if err == nil {
		t.Fatal("LoadFromBytes() error = nil, want error")
	}
}

func TestLoadFromBytesNormalizesWebPushSubscriber(t *testing.T) {
	t.Parallel()

	cfg, err := LoadFromBytes([]byte(`
database:
  dsn: "postgres://notify"
rabbitmq:
  url: "amqp://guest:guest@localhost:5672/"
redis:
  url: "redis://localhost:6379/0"
providers:
  web_push:
    enabled: true
    subscriber: "mailto:ops@example.com"
    vapid_public_key: "public"
    vapid_private_key: "private"
`))
	if err != nil {
		t.Fatalf("LoadFromBytes() error = %v", err)
	}
	if got := cfg.Providers.WebPush.Subscriber; got != "ops@example.com" {
		t.Fatalf("web push subscriber = %q, want bare email", got)
	}
}

func TestLoadFromBytesPreservesHTTPSWebPushSubscriber(t *testing.T) {
	t.Parallel()

	cfg, err := LoadFromBytes([]byte(`
database:
  dsn: "postgres://notify"
rabbitmq:
  url: "amqp://guest:guest@localhost:5672/"
redis:
  url: "redis://localhost:6379/0"
providers:
  web_push:
    enabled: true
    subscriber: "https://ops.example.com/web-push-contact"
    vapid_public_key: "public"
    vapid_private_key: "private"
`))
	if err != nil {
		t.Fatalf("LoadFromBytes() error = %v", err)
	}
	if got := cfg.Providers.WebPush.Subscriber; got != "https://ops.example.com/web-push-contact" {
		t.Fatalf("web push subscriber = %q, want https URL", got)
	}
}

func TestLoadFromBytesRejectsInvalidWebPushSubscriber(t *testing.T) {
	t.Parallel()

	_, err := LoadFromBytes([]byte(`
database:
  dsn: "postgres://notify"
rabbitmq:
  url: "amqp://guest:guest@localhost:5672/"
redis:
  url: "redis://localhost:6379/0"
providers:
  web_push:
    enabled: true
    subscriber: "http://ops.example.com"
    vapid_public_key: "public"
    vapid_private_key: "private"
`))
	if err == nil {
		t.Fatal("LoadFromBytes() error = nil, want error")
	}
}
