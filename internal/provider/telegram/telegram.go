package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ledatu/csar-notify/internal/config"
	"github.com/ledatu/csar-notify/internal/domain"
)

type botConfig struct {
	token      string
	apiBaseURL string
}

type Provider struct {
	client     *http.Client
	bots       map[string]botConfig
	defaultBot string
	logger     *slog.Logger
}

func New(client *http.Client, telegramCfg config.TelegramProviderConfig, logger *slog.Logger) (*Provider, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if logger == nil {
		logger = slog.Default()
	}
	bots, defaultBot, err := telegramCfg.EffectiveBots()
	if err != nil {
		return nil, err
	}
	resolved := make(map[string]botConfig, len(bots))
	for name, bot := range bots {
		apiBaseURL := strings.TrimRight(strings.TrimSpace(bot.APIBaseURL), "/")
		if apiBaseURL == "" {
			apiBaseURL = "https://api.telegram.org"
		}
		resolved[name] = botConfig{
			token:      bot.Token,
			apiBaseURL: apiBaseURL,
		}
	}
	return &Provider{
		client:     client,
		bots:       resolved,
		defaultBot: defaultBot,
		logger:     logger.With("component", "notify_provider_telegram"),
	}, nil
}

func (p *Provider) Name() string {
	return "telegram"
}

func (p *Provider) Channel() domain.Channel {
	return domain.ChannelTelegram
}

func (p *Provider) Send(ctx context.Context, n *domain.Notification, recipientUUID string, prefConfig json.RawMessage) error {
	var pref struct {
		ChatID string `json:"chat_id"`
		Bot    string `json:"bot"`
	}
	if len(prefConfig) > 0 {
		if err := json.Unmarshal(prefConfig, &pref); err != nil {
			return fmt.Errorf("decode telegram preference for %s: %w", recipientUUID, err)
		}
	}
	if strings.TrimSpace(pref.ChatID) == "" {
		return fmt.Errorf("telegram chat_id is missing for recipient %s", recipientUUID)
	}
	botName := strings.TrimSpace(pref.Bot)
	if botName == "" && n != nil && n.Metadata != nil {
		botName = strings.TrimSpace(n.Metadata["telegram_bot"])
	}
	if botName == "" {
		botName = p.defaultBot
	}
	bot, ok := p.bots[botName]
	if !ok {
		return fmt.Errorf("telegram bot %q is not configured", botName)
	}

	body, err := json.Marshal(map[string]string{
		"chat_id": pref.ChatID,
		"text":    renderMessage(n),
	})
	if err != nil {
		return fmt.Errorf("marshal telegram message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, bot.apiBaseURL+"/bot"+bot.token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 == 2 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("telegram send unexpected status %d: %s", resp.StatusCode, bytes.TrimSpace(payload))
}

func (p *Provider) Close() error {
	return nil
}

func renderMessage(n *domain.Notification) string {
	if n == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(n.Title)
	if strings.TrimSpace(n.Body) != "" {
		sb.WriteString("\n\n")
		sb.WriteString(n.Body)
	}
	if strings.TrimSpace(n.Link) != "" {
		sb.WriteString("\n\n")
		sb.WriteString(n.Link)
	}
	return sb.String()
}
