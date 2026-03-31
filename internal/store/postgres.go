package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ledatu/csar-core/pgutil"
	"github.com/ledatu/csar-notify/internal/domain"
)

var migrations = []pgutil.Migration{
	{
		Name: "001_notifications",
		Up: `
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS notifications (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    topic       TEXT NOT NULL,
    title       TEXT NOT NULL,
    body        TEXT NOT NULL DEFAULT '',
    link        TEXT NOT NULL DEFAULT '',
    metadata    JSONB NOT NULL DEFAULT '{}'::jsonb,
    priority    TEXT NOT NULL DEFAULT 'normal',
    broadcast   BOOLEAN NOT NULL DEFAULT FALSE,
    sender      TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS notification_deliveries (
    notification_id UUID NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    recipient       UUID NOT NULL,
    read            BOOLEAN NOT NULL DEFAULT FALSE,
    dismissed       BOOLEAN NOT NULL DEFAULT FALSE,
    read_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (notification_id, recipient)
);

CREATE INDEX IF NOT EXISTS idx_notification_deliveries_recipient_created
    ON notification_deliveries (recipient, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notification_deliveries_recipient_unread
    ON notification_deliveries (recipient, created_at DESC)
    WHERE NOT dismissed AND NOT read;
`,
	},
	{
		Name: "002_preferences_and_subscriptions",
		Up: `
CREATE TABLE IF NOT EXISTS notification_preferences (
    subject     UUID NOT NULL,
    channel     TEXT NOT NULL,
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    config      JSONB NOT NULL DEFAULT '{}'::jsonb,
    topics      TEXT[] NOT NULL DEFAULT '{}',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (subject, channel)
);

CREATE TABLE IF NOT EXISTS topic_subscriptions (
    subject     UUID NOT NULL,
    topic       TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (subject, topic)
);

CREATE INDEX IF NOT EXISTS idx_topic_subscriptions_topic
    ON topic_subscriptions (topic, subject);
`,
	},
}

type Postgres struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewPostgres(pool *pgxpool.Pool, logger *slog.Logger) *Postgres {
	if logger == nil {
		logger = slog.Default()
	}
	return &Postgres{pool: pool, logger: logger}
}

func (s *Postgres) Migrate(ctx context.Context) error {
	return pgutil.RunMigrations(ctx, s.pool, "notify_service_migrations", migrations, s.logger)
}

func (s *Postgres) UpsertNotification(ctx context.Context, n *domain.Notification) error {
	if n == nil {
		return fmt.Errorf("notification is required")
	}

	row := s.pool.QueryRow(ctx, `
INSERT INTO notifications (id, topic, title, body, link, metadata, priority, broadcast, sender)
VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()), $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE
SET topic = EXCLUDED.topic,
    title = EXCLUDED.title,
    body = EXCLUDED.body,
    link = EXCLUDED.link,
    metadata = EXCLUDED.metadata,
    priority = EXCLUDED.priority,
    broadcast = EXCLUDED.broadcast,
    sender = EXCLUDED.sender
RETURNING id::text, created_at
`,
		n.ID,
		n.Topic,
		n.Title,
		n.Body,
		n.Link,
		mapToJSON(n.Metadata),
		string(n.Priority),
		len(n.Recipients) == 0,
		n.Sender,
	)

	if err := row.Scan(&n.ID, &n.CreatedAt); err != nil {
		return fmt.Errorf("upsert notification: %w", err)
	}
	return nil
}

func (s *Postgres) InsertSiteDelivery(ctx context.Context, recipient string, n *domain.Notification) (*domain.Delivery, error) {
	if n == nil {
		return nil, fmt.Errorf("notification is required")
	}
	row := s.pool.QueryRow(ctx, `
INSERT INTO notification_deliveries (notification_id, recipient)
VALUES ($1::uuid, $2::uuid)
ON CONFLICT (notification_id, recipient) DO UPDATE
SET notification_id = EXCLUDED.notification_id
RETURNING read, dismissed, read_at, created_at
`, n.ID, recipient)

	item := &domain.Delivery{
		NotificationID: n.ID,
		Topic:          n.Topic,
		Title:          n.Title,
		Body:           n.Body,
		Link:           n.Link,
		Metadata:       cloneMetadata(n.Metadata),
		Priority:       n.Priority,
		Sender:         n.Sender,
		CreatedAt:      n.CreatedAt,
	}
	if err := row.Scan(&item.Read, &item.Dismissed, &item.ReadAt, &item.CreatedAt); err != nil {
		return nil, fmt.Errorf("insert site delivery: %w", err)
	}
	return item, nil
}

func (s *Postgres) ListTopicSubscribers(ctx context.Context, topic string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT subject::text FROM topic_subscriptions WHERE topic = $1 ORDER BY subject`, topic)
	if err != nil {
		return nil, fmt.Errorf("list topic subscribers: %w", err)
	}
	defer rows.Close()

	var subjects []string
	for rows.Next() {
		var subject string
		if err := rows.Scan(&subject); err != nil {
			return nil, fmt.Errorf("scan topic subscription: %w", err)
		}
		subjects = append(subjects, subject)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate topic subscriptions: %w", err)
	}
	return subjects, nil
}

func (s *Postgres) GetPreferences(ctx context.Context, subject string) ([]domain.Preference, error) {
	rows, err := s.pool.Query(ctx, `
SELECT channel, enabled, config, topics, updated_at
FROM notification_preferences
WHERE subject = $1::uuid
ORDER BY channel
`, subject)
	if err != nil {
		return nil, fmt.Errorf("get preferences: %w", err)
	}
	defer rows.Close()

	var prefs []domain.Preference
	for rows.Next() {
		var pref domain.Preference
		pref.Subject = subject
		if err := rows.Scan(&pref.Channel, &pref.Enabled, &pref.Config, &pref.Topics, &pref.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan preference: %w", err)
		}
		prefs = append(prefs, pref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate preferences: %w", err)
	}
	return prefs, nil
}

func (s *Postgres) UpsertPreference(ctx context.Context, subject string, pref *domain.Preference) error {
	if pref == nil {
		return fmt.Errorf("preference is required")
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO notification_preferences (subject, channel, enabled, config, topics, updated_at)
VALUES ($1::uuid, $2, $3, $4, $5, now())
ON CONFLICT (subject, channel) DO UPDATE
SET enabled = EXCLUDED.enabled,
    config = EXCLUDED.config,
    topics = EXCLUDED.topics,
    updated_at = now()
`, subject, string(pref.Channel), pref.Enabled, rawJSON(pref.Config), pref.Topics)
	if err != nil {
		return fmt.Errorf("upsert preference: %w", err)
	}
	return nil
}

func (s *Postgres) ListInbox(ctx context.Context, recipient string, limit int, offset int) ([]domain.Delivery, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.pool.Query(ctx, `
SELECT n.id::text, n.topic, n.title, n.body, n.link, n.metadata, n.priority, n.sender, n.created_at,
       d.read, d.dismissed, d.read_at
FROM notification_deliveries d
JOIN notifications n ON n.id = d.notification_id
WHERE d.recipient = $1::uuid AND NOT d.dismissed
ORDER BY n.created_at DESC, n.id DESC
LIMIT $2 OFFSET $3
`, recipient, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list inbox: %w", err)
	}
	defer rows.Close()

	items := make([]domain.Delivery, 0, limit)
	for rows.Next() {
		item, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate inbox: %w", err)
	}
	return items, nil
}

func (s *Postgres) CountUnread(ctx context.Context, recipient string) (int, error) {
	var count int
	if err := s.pool.QueryRow(ctx, `
SELECT count(*)
FROM notification_deliveries
WHERE recipient = $1::uuid AND NOT dismissed AND NOT read
`, recipient).Scan(&count); err != nil {
		return 0, fmt.Errorf("count unread: %w", err)
	}
	return count, nil
}

func (s *Postgres) MarkRead(ctx context.Context, recipient string, notificationID string) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE notification_deliveries
SET read = TRUE,
    read_at = COALESCE(read_at, now())
WHERE recipient = $1::uuid AND notification_id = $2::uuid
`, recipient, notificationID)
	if err != nil {
		return fmt.Errorf("mark read: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Postgres) Dismiss(ctx context.Context, recipient string, notificationID string) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE notification_deliveries
SET dismissed = TRUE
WHERE recipient = $1::uuid AND notification_id = $2::uuid
`, recipient, notificationID)
	if err != nil {
		return fmt.Errorf("dismiss notification: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func DefaultPreferences(subject string) []domain.Preference {
	return []domain.Preference{
		{
			Subject: subject,
			Channel: domain.ChannelSite,
			Enabled: true,
			Config:  json.RawMessage(`{}`),
		},
	}
}

func MergeDefaults(subject string, prefs []domain.Preference) []domain.Preference {
	out := DefaultPreferences(subject)
	index := map[domain.Channel]int{
		domain.ChannelSite: 0,
	}
	for _, pref := range prefs {
		if idx, ok := index[pref.Channel]; ok {
			out[idx] = pref
			continue
		}
		index[pref.Channel] = len(out)
		out = append(out, pref)
	}
	return out
}

func scanDelivery(rows pgx.Rows) (domain.Delivery, error) {
	var (
		item        domain.Delivery
		metadataRaw []byte
		priorityRaw string
	)
	if err := rows.Scan(
		&item.NotificationID,
		&item.Topic,
		&item.Title,
		&item.Body,
		&item.Link,
		&metadataRaw,
		&priorityRaw,
		&item.Sender,
		&item.CreatedAt,
		&item.Read,
		&item.Dismissed,
		&item.ReadAt,
	); err != nil {
		return domain.Delivery{}, fmt.Errorf("scan inbox delivery: %w", err)
	}
	item.Priority = domain.Priority(strings.TrimSpace(priorityRaw))
	item.Metadata = decodeMetadata(metadataRaw)
	return item, nil
}

func mapToJSON(data map[string]string) []byte {
	if len(data) == 0 {
		return []byte(`{}`)
	}
	b, err := json.Marshal(data)
	if err != nil {
		return []byte(`{}`)
	}
	return b
}

func rawJSON(data json.RawMessage) []byte {
	if len(data) == 0 {
		return []byte(`{}`)
	}
	return []byte(data)
}

func decodeMetadata(data []byte) map[string]string {
	if len(data) == 0 {
		return nil
	}
	var out map[string]string
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

func cloneMetadata(data map[string]string) map[string]string {
	if len(data) == 0 {
		return nil
	}
	out := make(map[string]string, len(data))
	for k, v := range data {
		out[k] = v
	}
	return out
}
