package webpush

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	libwebpush "github.com/SherClockHolmes/webpush-go"
	"github.com/golang-jwt/jwt/v5"

	"github.com/ledatu/csar-notify/internal/config"
	"github.com/ledatu/csar-notify/internal/domain"
	providerpkg "github.com/ledatu/csar-notify/internal/provider"
	"github.com/ledatu/csar-notify/internal/store"
)

type fakeSubscriptionStore struct {
	subscriptions []store.PushSubscriptionRow
	deleted       []string
}

func (s *fakeSubscriptionStore) ListPushSubscriptionsForSubject(_ context.Context, _ string) ([]store.PushSubscriptionRow, error) {
	return append([]store.PushSubscriptionRow(nil), s.subscriptions...), nil
}

func (s *fakeSubscriptionStore) DeletePushSubscription(_ context.Context, _, endpoint string) error {
	s.deleted = append(s.deleted, endpoint)
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) Do(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestBuildVAPIDAuthorizationHeaderUsesSingleMailtoPrefix(t *testing.T) {
	t.Parallel()

	privateKey, publicKey, err := libwebpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("GenerateVAPIDKeys() error = %v", err)
	}

	header, err := buildVAPIDAuthorizationHeader(
		"https://web.push.apple.com",
		"ops@example.com",
		publicKey,
		privateKey,
		time.Now().Add(12*time.Hour),
	)
	if err != nil {
		t.Fatalf("buildVAPIDAuthorizationHeader() error = %v", err)
	}

	tokenString := tokenFromAuthorizationHeader(t, header)
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		keyBytes, err := base64.RawURLEncoding.DecodeString(privateKey)
		if err != nil {
			return nil, err
		}
		signingKey, err := vapidPrivateKeyToECDSA(keyBytes)
		if err != nil {
			return nil, err
		}
		return signingKey.Public(), nil
	})
	if err != nil {
		t.Fatalf("jwt.Parse() error = %v", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		t.Fatalf("token valid = %v, claims type = %T", token.Valid, token.Claims)
	}
	if got := claims["sub"]; got != "mailto:ops@example.com" {
		t.Fatalf("sub claim = %v, want mailto:ops@example.com", got)
	}
}

func TestAuthHeaderCacheReusesHeadersUntilRefreshWindow(t *testing.T) {
	t.Parallel()

	privateKey, publicKey, err := libwebpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("GenerateVAPIDKeys() error = %v", err)
	}

	now := time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC)
	cache := newAuthHeaderCache()
	cache.now = func() time.Time { return now }

	header1, err := cache.authorizationHeader("https://web.push.apple.com/test", "ops@example.com", publicKey, privateKey)
	if err != nil {
		t.Fatalf("authorizationHeader() error = %v", err)
	}

	now = now.Add(2 * time.Hour)
	header2, err := cache.authorizationHeader("https://web.push.apple.com/test", "ops@example.com", publicKey, privateKey)
	if err != nil {
		t.Fatalf("authorizationHeader() second call error = %v", err)
	}
	if header1 != header2 {
		t.Fatal("expected cached authorization header to be reused")
	}

	now = now.Add(11 * time.Hour)
	header3, err := cache.authorizationHeader("https://web.push.apple.com/test", "ops@example.com", publicKey, privateKey)
	if err != nil {
		t.Fatalf("authorizationHeader() refresh call error = %v", err)
	}
	if header3 == header2 {
		t.Fatal("expected authorization header to refresh after cache window")
	}
}

func TestProviderSendHandlesAppleWebPushFailures(t *testing.T) {
	t.Parallel()

	privateKey, publicKey, err := libwebpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("GenerateVAPIDKeys() error = %v", err)
	}

	t.Run("expired subscriptions are deleted without retry", func(t *testing.T) {
		t.Parallel()

		store := &fakeSubscriptionStore{
			subscriptions: []store.PushSubscriptionRow{testPushSubscription("https://web.push.apple.com/expired")},
		}
		p := New(store, config.WebPushProviderConfig{
			Enabled:         true,
			Subscriber:      "ops@example.com",
			VAPIDPublicKey:  publicKey,
			VAPIDPrivateKey: privateKey,
		}, nil)
		p.httpClient = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusGone,
				Body:       io.NopCloser(strings.NewReader(`{"reason":"Unregistered"}`)),
			}, nil
		})

		err := p.Send(context.Background(), &domain.Notification{Topic: "support.reply", Title: "Reply"}, "00000000-0000-0000-0000-000000000001", nil)
		if err != nil {
			t.Fatalf("Send() error = %v, want nil", err)
		}
		if len(store.deleted) != 1 || store.deleted[0] != "https://web.push.apple.com/expired" {
			t.Fatalf("deleted subscriptions = %#v, want expired endpoint", store.deleted)
		}
	})

	t.Run("bad jwt token errors are treated as permanent web push failures", func(t *testing.T) {
		t.Parallel()

		store := &fakeSubscriptionStore{
			subscriptions: []store.PushSubscriptionRow{testPushSubscription("https://web.push.apple.com/badjwt")},
		}
		p := New(store, config.WebPushProviderConfig{
			Enabled:         true,
			Subscriber:      "ops@example.com",
			VAPIDPublicKey:  publicKey,
			VAPIDPrivateKey: privateKey,
		}, nil)
		p.httpClient = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(strings.NewReader(`{"reason":"BadJwtToken"}`)),
			}, nil
		})

		err := p.Send(context.Background(), &domain.Notification{Topic: "support.reply", Title: "Reply"}, "00000000-0000-0000-0000-000000000001", nil)
		if err == nil {
			t.Fatal("Send() error = nil, want permanent web push error")
		}
		if !providerpkg.IsPermanentChannelOnly(err, domain.ChannelWebPush) {
			t.Fatalf("Send() error = %v, want permanent web push classification", err)
		}
	})
}

func testPushSubscription(endpoint string) store.PushSubscriptionRow {
	return store.PushSubscriptionRow{
		Endpoint:  endpoint,
		P256dhKey: "BNNL5ZaTfK81qhXOx23-wewhigUeFb632jN6LvRWCFH1ubQr77FE_9qV1FuojuRmHP42zmf34rXgW80OvUVDgTk",
		AuthKey:   "zqbxT6JKstKSY9JKibZLSQ",
	}
}

func tokenFromAuthorizationHeader(t *testing.T, header string) string {
	t.Helper()

	parts := strings.Split(header, " ")
	if len(parts) < 3 {
		t.Fatalf("authorization header %q is malformed", header)
	}

	tokenParts := strings.Split(parts[1], "=")
	if len(tokenParts) < 2 {
		t.Fatalf("authorization header token part %q is malformed", parts[1])
	}

	return strings.TrimSuffix(tokenParts[1], ",")
}
