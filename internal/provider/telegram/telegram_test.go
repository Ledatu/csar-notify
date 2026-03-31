package telegram

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ledatu/csar-notify/internal/config"
	"github.com/ledatu/csar-notify/internal/domain"
)

func TestSendUsesNamedBotFromPreference(t *testing.T) {
	t.Parallel()

	var gotPath string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		gotBody = body
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	provider, err := New(server.Client(), config.TelegramProviderConfig{
		Enabled:    true,
		DefaultBot: "support",
		Bots: map[string]config.TelegramBotConfig{
			"support": {
				Token:      "support-token",
				APIBaseURL: server.URL,
			},
			"main-site": {
				Token:      "main-token",
				APIBaseURL: server.URL,
			},
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = provider.Send(context.Background(), &domain.Notification{
		Title: "Hello",
		Body:  "World",
	}, "recipient-1", json.RawMessage(`{"chat_id":"42","bot":"main-site"}`))
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if gotPath != "/botmain-token/sendMessage" {
		t.Fatalf("path = %q, want main bot path", gotPath)
	}
	if !strings.Contains(string(gotBody), `"chat_id":"42"`) {
		t.Fatalf("request body = %s, want chat_id", string(gotBody))
	}
}

func TestSendUsesMetadataBotOverride(t *testing.T) {
	t.Parallel()

	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	provider, err := New(server.Client(), config.TelegramProviderConfig{
		Enabled:  true,
		BotToken: "legacy-token",
		Bots: map[string]config.TelegramBotConfig{
			"main-site": {
				Token:      "main-token",
				APIBaseURL: server.URL,
			},
		},
		APIBaseURL: server.URL,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = provider.Send(context.Background(), &domain.Notification{
		Title:    "Hello",
		Metadata: map[string]string{"telegram_bot": "main-site"},
	}, "recipient-1", json.RawMessage(`{"chat_id":"42"}`))
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if gotPath != "/botmain-token/sendMessage" {
		t.Fatalf("path = %q, want metadata-selected bot path", gotPath)
	}
}
