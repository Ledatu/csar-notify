package preferences

import (
	"context"
	"net/http"
	"strings"

	csarerrors "github.com/ledatu/csar-core/errors"
	"github.com/ledatu/csar-core/httpx"
	"github.com/ledatu/csar-notify/internal/domain"
	"github.com/ledatu/csar-notify/internal/httpauth"
	"github.com/ledatu/csar-notify/internal/store"
)

type storeAccessor interface {
	GetPreferences(ctx context.Context, subject string) ([]domain.Preference, error)
	UpsertPreference(ctx context.Context, subject string, pref *domain.Preference) error
}

type Handler struct {
	store storeAccessor
}

func New(store storeAccessor) *Handler {
	return &Handler{store: store}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /notifications/preferences", h.list)
	mux.HandleFunc("PUT /notifications/preferences", h.put)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	subject, err := httpauth.SubjectFromRequest(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	prefs, err := h.store.GetPreferences(r.Context(), subject)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items": store.MergeDefaults(subject, prefs),
	})
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	subject, err := httpauth.SubjectFromRequest(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	var pref domain.Preference
	if err := httpx.ReadJSON(r, &pref); err != nil {
		httpx.WriteError(w, err)
		return
	}
	pref.Channel = domain.Channel(strings.TrimSpace(string(pref.Channel)))
	if !pref.Channel.Valid() {
		httpx.WriteError(w, csarerrors.Validation("channel is invalid"))
		return
	}
	pref.Topics = normalizeTopics(pref.Topics)
	if len(pref.Config) == 0 {
		pref.Config = []byte(`{}`)
	}
	if err := h.store.UpsertPreference(r.Context(), subject, &pref); err != nil {
		httpx.WriteError(w, err)
		return
	}

	prefs, err := h.store.GetPreferences(r.Context(), subject)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items": store.MergeDefaults(subject, prefs),
	})
}

func normalizeTopics(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, topic := range in {
		topic = strings.TrimSpace(topic)
		if topic == "" {
			continue
		}
		if _, ok := seen[topic]; ok {
			continue
		}
		seen[topic] = struct{}{}
		out = append(out, topic)
	}
	return out
}
