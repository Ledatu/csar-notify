package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Channel string

const (
	ChannelSite     Channel = "site"
	ChannelTelegram Channel = "telegram"
	ChannelEmail    Channel = "email"
)

type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
	PriorityUrgent Priority = "urgent"
)

type Notification struct {
	ID         string            `json:"id,omitempty"`
	Topic      string            `json:"topic"`
	Title      string            `json:"title"`
	Body       string            `json:"body,omitempty"`
	Link       string            `json:"link,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Priority   Priority          `json:"priority,omitempty"`
	Recipients []string          `json:"recipients,omitempty"`
	Channels   []Channel         `json:"channels,omitempty"`
	Sender     string            `json:"sender,omitempty"`
	CreatedAt  time.Time         `json:"-"`
}

type Delivery struct {
	NotificationID string            `json:"notification_id"`
	Topic          string            `json:"topic"`
	Title          string            `json:"title"`
	Body           string            `json:"body"`
	Link           string            `json:"link"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	Priority       Priority          `json:"priority"`
	Sender         string            `json:"sender"`
	CreatedAt      time.Time         `json:"created_at"`
	Read           bool              `json:"read"`
	Dismissed      bool              `json:"dismissed"`
	ReadAt         *time.Time        `json:"read_at,omitempty"`
}

type Preference struct {
	Subject   string          `json:"subject,omitempty"`
	Channel   Channel         `json:"channel"`
	Enabled   bool            `json:"enabled"`
	Config    json.RawMessage `json:"config,omitempty"`
	Topics    []string        `json:"topics,omitempty"`
	UpdatedAt time.Time       `json:"updated_at,omitempty"`
}

type ResolvedPreference struct {
	Channel Channel
	Config  json.RawMessage
}

func (n *Notification) Normalize() {
	if n == nil {
		return
	}
	n.Topic = strings.TrimSpace(n.Topic)
	n.Title = strings.TrimSpace(n.Title)
	n.Body = strings.TrimSpace(n.Body)
	n.Link = strings.TrimSpace(n.Link)
	n.Sender = strings.TrimSpace(n.Sender)
	n.Recipients = uniqueStrings(n.Recipients)
	n.Channels = uniqueChannels(n.Channels)
	if n.Priority == "" {
		n.Priority = PriorityNormal
	}
}

func ValidateNotification(n *Notification) error {
	if n == nil {
		return fmt.Errorf("notification is required")
	}
	n.Normalize()
	if n.ID != "" {
		if _, err := uuid.Parse(n.ID); err != nil {
			return fmt.Errorf("notification id must be a valid UUID")
		}
	}
	if n.Topic == "" {
		return fmt.Errorf("topic is required")
	}
	if n.Title == "" {
		return fmt.Errorf("title is required")
	}
	if !n.Priority.Valid() {
		return fmt.Errorf("priority %q is invalid", n.Priority)
	}
	for _, recipient := range n.Recipients {
		if _, err := uuid.Parse(recipient); err != nil {
			return fmt.Errorf("recipient %q must be a valid UUID", recipient)
		}
	}
	for _, channel := range n.Channels {
		if !channel.Valid() {
			return fmt.Errorf("channel %q is invalid", channel)
		}
	}
	return nil
}

func (c Channel) Valid() bool {
	switch c {
	case ChannelSite, ChannelTelegram, ChannelEmail:
		return true
	default:
		return false
	}
}

func (p Priority) Valid() bool {
	switch p {
	case PriorityLow, PriorityNormal, PriorityHigh, PriorityUrgent:
		return true
	default:
		return false
	}
}

func TopicMatches(filters []string, topic string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, filter := range filters {
		filter = strings.TrimSpace(filter)
		if filter == "" {
			continue
		}
		if filter == topic {
			return true
		}
		if strings.HasSuffix(filter, "*") && strings.HasPrefix(topic, strings.TrimSuffix(filter, "*")) {
			return true
		}
	}
	return false
}

func uniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, value := range in {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func uniqueChannels(in []Channel) []Channel {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[Channel]struct{}, len(in))
	out := make([]Channel, 0, len(in))
	for _, value := range in {
		value = Channel(strings.TrimSpace(string(value)))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
