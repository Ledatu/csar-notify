package webpush

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	libwebpush "github.com/SherClockHolmes/webpush-go"
	"github.com/golang-jwt/jwt/v5"
)

const (
	vapidAuthLifetime    = 12 * time.Hour
	vapidAuthRefreshSkew = 15 * time.Minute
)

type authHTTPClient struct {
	base       libwebpush.HTTPClient
	cache      *authHeaderCache
	subscriber string
	publicKey  string
	privateKey string
}

type authHeaderCache struct {
	mu      sync.Mutex
	now     func() time.Time
	headers map[string]cachedAuthorizationHeader
}

type cachedAuthorizationHeader struct {
	header       string
	refreshAfter time.Time
}

func newAuthHTTPClient(base libwebpush.HTTPClient, subscriber, publicKey, privateKey string) libwebpush.HTTPClient {
	if base == nil {
		base = &http.Client{}
	}
	return &authHTTPClient{
		base:       base,
		cache:      newAuthHeaderCache(),
		subscriber: strings.TrimSpace(subscriber),
		publicKey:  strings.TrimSpace(publicKey),
		privateKey: strings.TrimSpace(privateKey),
	}
}

func newAuthHeaderCache() *authHeaderCache {
	return &authHeaderCache{
		now:     time.Now,
		headers: make(map[string]cachedAuthorizationHeader),
	}
}

func (c *authHTTPClient) Do(req *http.Request) (*http.Response, error) {
	header, err := c.cache.authorizationHeader(req.URL.String(), c.subscriber, c.publicKey, c.privateKey)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", header)
	return c.base.Do(req)
}

func (c *authHeaderCache) authorizationHeader(endpoint, subscriber, publicKey, privateKey string) (string, error) {
	audience, err := pushServiceAudience(endpoint)
	if err != nil {
		return "", err
	}

	cacheKey := audience + "\n" + subscriber + "\n" + publicKey
	now := c.now()

	c.mu.Lock()
	if cached, ok := c.headers[cacheKey]; ok && now.Before(cached.refreshAfter) {
		c.mu.Unlock()
		return cached.header, nil
	}
	c.mu.Unlock()

	expiration := now.Add(vapidAuthLifetime)
	header, err := buildVAPIDAuthorizationHeader(audience, subscriber, publicKey, privateKey, expiration)
	if err != nil {
		return "", err
	}

	refreshAfter := expiration.Add(-vapidAuthRefreshSkew)
	c.mu.Lock()
	c.headers[cacheKey] = cachedAuthorizationHeader{
		header:       header,
		refreshAfter: refreshAfter,
	}
	c.mu.Unlock()

	return header, nil
}

func buildVAPIDAuthorizationHeader(audience, subscriber, publicKey, privateKey string, expiration time.Time) (string, error) {
	if expiration.IsZero() {
		expiration = time.Now().Add(vapidAuthLifetime)
	}

	subject, err := vapidSubject(subscriber)
	if err != nil {
		return "", err
	}

	decodedPrivateKey, err := decodeVAPIDKey(privateKey)
	if err != nil {
		return "", fmt.Errorf("decode VAPID private key: %w", err)
	}
	decodedPublicKey, err := decodeVAPIDKey(publicKey)
	if err != nil {
		return "", fmt.Errorf("decode VAPID public key: %w", err)
	}

	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"aud": audience,
		"exp": expiration.Unix(),
		"sub": subject,
	})
	signingKey, err := vapidPrivateKeyToECDSA(decodedPrivateKey)
	if err != nil {
		return "", fmt.Errorf("derive VAPID signing key: %w", err)
	}
	jwtString, err := token.SignedString(signingKey)
	if err != nil {
		return "", fmt.Errorf("sign VAPID JWT: %w", err)
	}

	return "vapid t=" + jwtString + ", k=" + base64.RawURLEncoding.EncodeToString(decodedPublicKey), nil
}

func pushServiceAudience(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("parse push endpoint: %w", err)
	}
	return u.Scheme + "://" + u.Host, nil
}

func vapidSubject(subscriber string) (string, error) {
	subscriber = strings.TrimSpace(subscriber)
	switch {
	case subscriber == "":
		return "", fmt.Errorf("subscriber is required")
	case strings.HasPrefix(strings.ToLower(subscriber), "https://"):
		return subscriber, nil
	case strings.HasPrefix(strings.ToLower(subscriber), "mailto:"):
		value := strings.TrimSpace(subscriber[len("mailto:"):])
		if value == "" {
			return "", fmt.Errorf("subscriber mailto target is required")
		}
		return "mailto:" + value, nil
	default:
		return "mailto:" + subscriber, nil
	}
}

func decodeVAPIDKey(key string) ([]byte, error) {
	key = strings.TrimSpace(key)
	if decoded, err := base64.RawURLEncoding.DecodeString(key); err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(key)
}

func vapidPrivateKeyToECDSA(privateKey []byte) (*ecdsa.PrivateKey, error) {
	p256 := ecdh.P256()
	ecdhPrivateKey, err := p256.NewPrivateKey(privateKey)
	if err != nil {
		return nil, err
	}

	curve := elliptic.P256()
	publicKey := ecdhPrivateKey.PublicKey().Bytes()
	coordSize := curve.Params().BitSize / 8
	if len(publicKey) != 1+coordSize*2 || publicKey[0] != 4 {
		return nil, fmt.Errorf("decode VAPID public key")
	}
	px := new(big.Int).SetBytes(publicKey[1 : 1+coordSize])
	py := new(big.Int).SetBytes(publicKey[1+coordSize:])
	d := new(big.Int).SetBytes(privateKey)
	return &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{
			Curve: curve,
			X:     px,
			Y:     py,
		},
		D: d,
	}, nil
}
