package inbox

import (
	"context"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	csarerrors "github.com/ledatu/csar-core/errors"
	"github.com/ledatu/csar-core/httpx"
	"github.com/ledatu/csar-notify/internal/domain"
	"github.com/ledatu/csar-notify/internal/httpauth"
)

type store interface {
	ListInbox(ctx context.Context, recipient string, limit int, offset int) ([]domain.Delivery, error)
	CountUnread(ctx context.Context, recipient string) (int, error)
	MarkRead(ctx context.Context, recipient string, notificationID string) error
	Dismiss(ctx context.Context, recipient string, notificationID string) error
}

type Handler struct {
	store store
}

func New(store store) *Handler {
	return &Handler{store: store}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /notifications", h.list)
	mux.HandleFunc("GET /notifications/count", h.count)
	mux.HandleFunc("PATCH /notifications/{id}/read", h.markRead)
	mux.HandleFunc("PATCH /notifications/{id}/dismiss", h.dismiss)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	subject, err := httpauth.SubjectFromRequest(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	limit, offset, err := pageParams(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	items, err := h.store.ListInbox(r.Context(), subject, limit, offset)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":  items,
		"limit":  limit,
		"offset": offset,
	})
}

func (h *Handler) count(w http.ResponseWriter, r *http.Request) {
	subject, err := httpauth.SubjectFromRequest(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	count, err := h.store.CountUnread(r.Context(), subject)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"unread": count})
}

func (h *Handler) markRead(w http.ResponseWriter, r *http.Request) {
	h.updateState(w, r, func(ctx context.Context, subject string, id string) error {
		return h.store.MarkRead(ctx, subject, id)
	})
}

func (h *Handler) dismiss(w http.ResponseWriter, r *http.Request) {
	h.updateState(w, r, func(ctx context.Context, subject string, id string) error {
		return h.store.Dismiss(ctx, subject, id)
	})
}

func (h *Handler) updateState(w http.ResponseWriter, r *http.Request, fn func(context.Context, string, string) error) {
	subject, err := httpauth.SubjectFromRequest(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.WriteError(w, csarerrors.Validation("notification id must be a valid UUID"))
		return
	}
	if err := fn(r.Context(), subject, id); err != nil {
		if err == pgx.ErrNoRows {
			httpx.WriteError(w, csarerrors.NotFound("notification not found"))
			return
		}
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func pageParams(r *http.Request) (int, int, error) {
	limit := 50
	offset := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 100 {
			return 0, 0, csarerrors.Validation("limit must be between 1 and 100")
		}
		limit = v
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			return 0, 0, csarerrors.Validation("offset must be >= 0")
		}
		offset = v
	}
	return limit, offset, nil
}
