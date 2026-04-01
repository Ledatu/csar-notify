package config

import (
	"fmt"
	"net/mail"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/ledatu/csar-core/configutil"
	"github.com/ledatu/csar-core/tlsx"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Service   ServiceConfig   `yaml:"service"`
	TLS       TLSSection      `yaml:"tls"`
	Database  DatabaseConfig  `yaml:"database"`
	RabbitMQ  RabbitMQConfig  `yaml:"rabbitmq"`
	Redis     RedisConfig     `yaml:"redis"`
	Ingest    IngestConfig    `yaml:"ingest"`
	Consumer  ConsumerConfig  `yaml:"consumer"`
	Providers ProvidersConfig `yaml:"providers"`
	HTTP      HTTPExtraConfig `yaml:"http"`
	Tracing   TracingConfig   `yaml:"tracing"`
}

type ServiceConfig struct {
	Name       string `yaml:"name"`
	Port       int    `yaml:"port"`
	HealthPort int    `yaml:"health_port"`
}

type TLSSection struct {
	CertFile     string `yaml:"cert_file"`
	KeyFile      string `yaml:"key_file"`
	ClientCAFile string `yaml:"client_ca_file"`
	MinVersion   string `yaml:"min_version"`
}

type DatabaseConfig struct {
	DSN string `yaml:"dsn"`
}

type RabbitMQConfig struct {
	URL            string              `yaml:"url"`
	ReconnectDelay configutil.Duration `yaml:"reconnect_delay"`
}

type RedisConfig struct {
	URL    string `yaml:"url"`
	Prefix string `yaml:"prefix"`
}

type IngestConfig struct {
	BufferSize       int `yaml:"buffer_size"`
	PublisherWorkers int `yaml:"publisher_workers"`
}

type ConsumerConfig struct {
	Queue           ConsumerQueueConfig `yaml:"queue"`
	DLQ             ConsumerDLQConfig   `yaml:"dlq"`
	BatchSize       int                 `yaml:"batch_size"`
	FlushInterval   configutil.Duration `yaml:"flush_interval"`
	MaxRedeliveries int                 `yaml:"max_redeliveries"`
}

type ConsumerQueueConfig struct {
	Name     string `yaml:"name"`
	Durable  bool   `yaml:"durable"`
	Prefetch int    `yaml:"prefetch"`
}

type ConsumerDLQConfig struct {
	Name    string `yaml:"name"`
	Durable bool   `yaml:"durable"`
}

type ProvidersConfig struct {
	Site     SiteProviderConfig     `yaml:"site"`
	Telegram TelegramProviderConfig `yaml:"telegram"`
	Email    EmailProviderConfig    `yaml:"email"`
	WebPush  WebPushProviderConfig  `yaml:"web_push"`
}

type SiteProviderConfig struct {
	Enabled       bool                        `yaml:"enabled"`
	DefaultTarget string                      `yaml:"default_target"`
	Targets       map[string]SiteTargetConfig `yaml:"targets"`
}

type SiteTargetConfig struct {
	RedisPrefix string `yaml:"redis_prefix"`
}

type TelegramProviderConfig struct {
	Enabled    bool                         `yaml:"enabled"`
	DefaultBot string                       `yaml:"default_bot"`
	BotToken   string                       `yaml:"bot_token"`
	APIBaseURL string                       `yaml:"api_base_url"`
	Bots       map[string]TelegramBotConfig `yaml:"bots"`
}

type TelegramBotConfig struct {
	Token      string `yaml:"token"`
	APIBaseURL string `yaml:"api_base_url"`
}

type EmailProviderConfig struct {
	Enabled bool `yaml:"enabled"`
}

// WebPushProviderConfig configures encrypted Web Push (VAPID) delivery to browsers.
type WebPushProviderConfig struct {
	Enabled         bool   `yaml:"enabled"`
	Subscriber      string `yaml:"subscriber"` // bare e-mail or https URL used for the VAPID JWT "sub" claim
	VAPIDPublicKey  string `yaml:"vapid_public_key"`
	VAPIDPrivateKey string `yaml:"vapid_private_key"`
}

type HTTPExtraConfig struct {
	AllowedClientCN string `yaml:"allowed_client_cn"`
}

type TracingConfig struct {
	Endpoint    string  `yaml:"endpoint"`
	SampleRatio float64 `yaml:"sample_ratio"`
}

func LoadFromBytes(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	configutil.ExpandEnvInStruct(reflect.ValueOf(&cfg).Elem())
	defaults(&cfg)
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func defaults(cfg *Config) {
	if cfg.Service.Name == "" {
		cfg.Service.Name = "csar-notify"
	}
	if cfg.Service.Port == 0 {
		cfg.Service.Port = 8085
	}
	if cfg.Service.HealthPort == 0 {
		cfg.Service.HealthPort = 9085
	}
	if cfg.Redis.Prefix == "" {
		cfg.Redis.Prefix = "notify:"
	}
	if cfg.Ingest.BufferSize == 0 {
		cfg.Ingest.BufferSize = 10_000
	}
	if cfg.Ingest.PublisherWorkers == 0 {
		cfg.Ingest.PublisherWorkers = 4
	}
	if cfg.Consumer.Queue.Name == "" {
		cfg.Consumer.Queue.Name = "notify.events"
	}
	if cfg.Consumer.Queue.Prefetch == 0 {
		cfg.Consumer.Queue.Prefetch = 200
	}
	if cfg.Consumer.DLQ.Name == "" {
		cfg.Consumer.DLQ.Name = "notify.events.dlq"
	}
	if cfg.Consumer.BatchSize == 0 {
		cfg.Consumer.BatchSize = 200
	}
	if cfg.Consumer.FlushInterval.Std() == 0 {
		cfg.Consumer.FlushInterval.Duration = 2 * time.Second
	}
	if cfg.Consumer.MaxRedeliveries == 0 {
		cfg.Consumer.MaxRedeliveries = 3
	}
	if cfg.Providers.Telegram.APIBaseURL == "" {
		cfg.Providers.Telegram.APIBaseURL = "https://api.telegram.org"
	}
	for name, bot := range cfg.Providers.Telegram.Bots {
		if strings.TrimSpace(bot.APIBaseURL) == "" {
			bot.APIBaseURL = cfg.Providers.Telegram.APIBaseURL
			cfg.Providers.Telegram.Bots[name] = bot
		}
	}
	if cfg.Tracing.SampleRatio == 0 {
		cfg.Tracing.SampleRatio = 1.0
	}
}

func (c *Config) validate() error {
	if c.Database.DSN == "" {
		return fmt.Errorf("database.dsn is required")
	}
	if c.RabbitMQ.URL == "" {
		return fmt.Errorf("rabbitmq.url is required")
	}
	if c.Redis.URL == "" {
		return fmt.Errorf("redis.url is required")
	}
	if c.Ingest.BufferSize < 1 {
		return fmt.Errorf("ingest.buffer_size must be >= 1")
	}
	if c.Ingest.PublisherWorkers < 1 {
		return fmt.Errorf("ingest.publisher_workers must be >= 1")
	}
	if c.Consumer.BatchSize < 1 {
		return fmt.Errorf("consumer.batch_size must be >= 1")
	}
	if c.Consumer.Queue.Name == "" {
		return fmt.Errorf("consumer.queue.name is required")
	}
	if c.Consumer.DLQ.Name == "" {
		return fmt.Errorf("consumer.dlq.name is required")
	}
	if c.Providers.Email.Enabled {
		return fmt.Errorf("providers.email is not implemented yet")
	}
	if c.Providers.Site.Enabled {
		if _, _, err := c.Providers.Site.EffectiveTargets(c.Redis.Prefix); err != nil {
			return err
		}
	}
	if c.Providers.Telegram.Enabled {
		if _, _, err := c.Providers.Telegram.EffectiveBots(); err != nil {
			return err
		}
	}
	if c.Providers.WebPush.Enabled {
		subscriber, err := normalizeWebPushSubscriber(c.Providers.WebPush.Subscriber)
		if err != nil {
			return err
		}
		c.Providers.WebPush.Subscriber = subscriber
		if strings.TrimSpace(c.Providers.WebPush.VAPIDPublicKey) == "" || strings.TrimSpace(c.Providers.WebPush.VAPIDPrivateKey) == "" {
			return fmt.Errorf("providers.web_push.vapid_public_key and vapid_private_key are required when web_push is enabled")
		}
	}
	return nil
}

func (c *Config) HTTPAddr() string {
	return fmt.Sprintf(":%d", c.Service.Port)
}

func (c *Config) HealthAddr() string {
	return fmt.Sprintf(":%d", c.Service.HealthPort)
}

func (t TLSSection) ToTLSx() tlsx.ServerConfig {
	return tlsx.ServerConfig{
		CertFile:     t.CertFile,
		KeyFile:      t.KeyFile,
		ClientCAFile: t.ClientCAFile,
		MinVersion:   t.MinVersion,
	}
}

func (s SiteProviderConfig) EffectiveTargets(basePrefix string) (map[string]SiteTargetConfig, string, error) {
	targets := make(map[string]SiteTargetConfig, len(s.Targets))
	for rawName, rawTarget := range s.Targets {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, "", fmt.Errorf("providers.site.targets keys must not be empty")
		}
		target := SiteTargetConfig{
			RedisPrefix: strings.TrimSpace(rawTarget.RedisPrefix),
		}
		targets[name] = target
	}

	defaultTarget := strings.TrimSpace(s.DefaultTarget)
	if len(targets) == 0 {
		if defaultTarget == "" {
			defaultTarget = "default"
		}
		targets[defaultTarget] = SiteTargetConfig{RedisPrefix: basePrefix}
		return targets, defaultTarget, nil
	}

	if defaultTarget == "" {
		if len(targets) != 1 {
			return nil, "", fmt.Errorf("providers.site.default_target is required when multiple site targets are configured")
		}
		for name := range targets {
			defaultTarget = name
		}
	}
	if _, ok := targets[defaultTarget]; !ok {
		return nil, "", fmt.Errorf("providers.site.default_target %q is not defined under providers.site.targets", defaultTarget)
	}

	for name, target := range targets {
		if target.RedisPrefix == "" {
			if len(targets) == 1 {
				target.RedisPrefix = basePrefix
			} else {
				target.RedisPrefix = basePrefix + name + ":"
			}
			targets[name] = target
		}
	}

	return targets, defaultTarget, nil
}

func (t TelegramProviderConfig) EffectiveBots() (map[string]TelegramBotConfig, string, error) {
	const legacyBotName = "default"

	bots := make(map[string]TelegramBotConfig, len(t.Bots)+1)
	for rawName, rawBot := range t.Bots {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, "", fmt.Errorf("providers.telegram.bots keys must not be empty")
		}
		bot := TelegramBotConfig{
			Token:      strings.TrimSpace(rawBot.Token),
			APIBaseURL: strings.TrimSpace(rawBot.APIBaseURL),
		}
		if bot.Token == "" {
			return nil, "", fmt.Errorf("providers.telegram.bots.%s.token is required", name)
		}
		if bot.APIBaseURL == "" {
			bot.APIBaseURL = "https://api.telegram.org"
		}
		bots[name] = bot
	}

	if token := strings.TrimSpace(t.BotToken); token != "" {
		apiBaseURL := strings.TrimSpace(t.APIBaseURL)
		if apiBaseURL == "" {
			apiBaseURL = "https://api.telegram.org"
		}
		bots[legacyBotName] = TelegramBotConfig{
			Token:      token,
			APIBaseURL: apiBaseURL,
		}
	}

	if len(bots) == 0 {
		return nil, "", fmt.Errorf("providers.telegram requires bot_token or bots when telegram is enabled")
	}

	defaultBot := strings.TrimSpace(t.DefaultBot)
	if defaultBot == "" {
		if _, ok := bots[legacyBotName]; ok {
			defaultBot = legacyBotName
		} else if len(bots) == 1 {
			for name := range bots {
				defaultBot = name
			}
		} else {
			return nil, "", fmt.Errorf("providers.telegram.default_bot is required when multiple bots are configured")
		}
	}
	if _, ok := bots[defaultBot]; !ok {
		return nil, "", fmt.Errorf("providers.telegram.default_bot %q is not defined", defaultBot)
	}

	return bots, defaultBot, nil
}

func normalizeWebPushSubscriber(raw string) (string, error) {
	subscriber := strings.TrimSpace(raw)
	if subscriber == "" {
		return "", fmt.Errorf("providers.web_push.subscriber is required when web_push is enabled (e.g. ops@example.com or https://example.com/contact)")
	}

	if strings.HasPrefix(strings.ToLower(subscriber), "mailto:") {
		subscriber = strings.TrimSpace(subscriber[len("mailto:"):])
	}

	if strings.HasPrefix(strings.ToLower(subscriber), "https://") {
		u, err := url.Parse(subscriber)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return "", fmt.Errorf("providers.web_push.subscriber must be a bare e-mail, mailto: e-mail, or https URL")
		}
		return u.String(), nil
	}

	addr, err := mail.ParseAddress(subscriber)
	if err != nil || !strings.EqualFold(strings.TrimSpace(addr.Address), subscriber) {
		return "", fmt.Errorf("providers.web_push.subscriber must be a bare e-mail, mailto: e-mail, or https URL")
	}
	return addr.Address, nil
}
