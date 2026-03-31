package rmq

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	amqp091 "github.com/rabbitmq/amqp091-go"
)

const defaultReconnectDelay = 5 * time.Second

type ConnectionConfig struct {
	URL            string
	ReconnectDelay time.Duration
}

type ConnectionManager struct {
	url            string
	reconnectDelay time.Duration
	logger         *slog.Logger

	mu   sync.RWMutex
	conn *amqp091.Connection

	closeCh chan struct{}
	done    chan struct{}
}

func NewConnectionManager(cfg ConnectionConfig, logger *slog.Logger) *ConnectionManager {
	delay := cfg.ReconnectDelay
	if delay == 0 {
		delay = defaultReconnectDelay
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ConnectionManager{
		url:            cfg.URL,
		reconnectDelay: delay,
		logger:         logger,
		closeCh:        make(chan struct{}),
		done:           make(chan struct{}),
	}
}

func (cm *ConnectionManager) Connect(ctx context.Context) error {
	if err := cm.dial(ctx); err != nil {
		return fmt.Errorf("amqp initial connect: %w", err)
	}
	go cm.reconnectLoop()
	return nil
}

func (cm *ConnectionManager) Channel() (*amqp091.Channel, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	if cm.conn == nil || cm.conn.IsClosed() {
		return nil, fmt.Errorf("amqp: connection not available")
	}
	return cm.conn.Channel()
}

func (cm *ConnectionManager) Close() error {
	close(cm.closeCh)
	<-cm.done
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.conn != nil && !cm.conn.IsClosed() {
		return cm.conn.Close()
	}
	return nil
}

func (cm *ConnectionManager) IsConnected() bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.conn != nil && !cm.conn.IsClosed()
}

func (cm *ConnectionManager) dial(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	conn, err := amqp091.DialConfig(cm.url, amqp091.Config{
		Heartbeat: 10 * time.Second,
		Locale:    "en_US",
	})
	if err != nil {
		return err
	}
	cm.mu.Lock()
	cm.conn = conn
	cm.mu.Unlock()
	cm.logger.Info("amqp connected", "url", sanitizeURL(cm.url))
	return nil
}

func (cm *ConnectionManager) reconnectLoop() {
	defer close(cm.done)
	for {
		cm.mu.RLock()
		conn := cm.conn
		cm.mu.RUnlock()
		if conn == nil {
			return
		}
		notifyCh := conn.NotifyClose(make(chan *amqp091.Error, 1))

		select {
		case <-cm.closeCh:
			return
		case amqpErr := <-notifyCh:
			if amqpErr != nil {
				cm.logger.Warn("amqp connection lost", "error", amqpErr.Error())
			}
		}

		delay := cm.reconnectDelay
		for {
			select {
			case <-cm.closeCh:
				return
			case <-time.After(delay):
			}

			cm.logger.Info("amqp reconnecting", "delay", delay.String())
			if err := cm.dial(context.Background()); err != nil {
				cm.logger.Error("amqp reconnect failed", "error", err.Error())
				delay = min(delay*2, 60*time.Second)
				continue
			}
			break
		}
	}
}

func sanitizeURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "***"
	}
	u.User = nil
	return u.Redacted()
}
