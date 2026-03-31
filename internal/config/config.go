package config

import (
	"fmt"
	"reflect"
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
}

type SiteProviderConfig struct {
	Enabled bool `yaml:"enabled"`
}

type TelegramProviderConfig struct {
	Enabled    bool   `yaml:"enabled"`
	BotToken   string `yaml:"bot_token"`
	APIBaseURL string `yaml:"api_base_url"`
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
	if c.Providers.Telegram.Enabled && c.Providers.Telegram.BotToken == "" {
		return fmt.Errorf("providers.telegram.bot_token is required when telegram is enabled")
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
