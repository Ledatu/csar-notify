package pushsubscription

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	csarerrors "github.com/ledatu/csar-core/errors"
	"github.com/ledatu/csar-core/httpx"
	"github.com/ledatu/csar-notify/internal/httpauth"
)

type store interface {
	UpsertPushSubscription(ctx context.Context, subject, endpoint, p256dh, auth, userAgent string) error
	DeletePushSubscription(ctx context.Context, subject, endpoint string) error
}

type subscribeRequest struct {
	// Raw JSON: PushSubscription includes expirationTime and other fields we must ignore.
	Subscription json.RawMessage `json:"subscription"`
	UserAgent    string          `json:"user_agent"`
}

type deleteRequest struct {
	Endpoint string `json:"endpoint"`
}

type Handler struct {
	store store
}

func New(s store) *Handler {
	return &Handler{store: s}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /notifications/push/subscriptions", h.post)
	mux.HandleFunc("DELETE /notifications/push/subscriptions", h.delete)
}

func (h *Handler) post(w http.ResponseWriter, r *http.Request) {
	subject, err := httpauth.SubjectFromRequest(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var req subscribeRequest
	if err := httpx.ReadJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	var sub struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(req.Subscription, &sub); err != nil {
		httpx.WriteError(w, csarerrors.Validation("invalid subscription JSON"))
		return
	}
	endpoint := strings.TrimSpace(sub.Endpoint)
	p256dh := strings.TrimSpace(sub.Keys.P256dh)
	auth := strings.TrimSpace(sub.Keys.Auth)
	if endpoint == "" || p256dh == "" || auth == "" {
		httpx.WriteError(w, csarerrors.Validation("subscription.endpoint and keys are required"))
		return
	}
	if err := h.store.UpsertPushSubscription(r.Context(), subject, endpoint, p256dh, auth, strings.TrimSpace(req.UserAgent)); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	subject, err := httpauth.SubjectFromRequest(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var req deleteRequest
	if err := httpx.ReadJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	endpoint := strings.TrimSpace(req.Endpoint)
	if endpoint == "" {
		httpx.WriteError(w, csarerrors.Validation("endpoint is required"))
		return
	}
	if err := h.store.DeletePushSubscription(r.Context(), subject, endpoint); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
