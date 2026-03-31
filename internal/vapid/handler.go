package vapid

import (
	"net/http"

	"github.com/ledatu/csar-core/httpx"
)

// Handler exposes the VAPID public key for browser PushManager.subscribe.
type Handler struct {
	PublicKey string
}

func New(publicKey string) *Handler {
	return &Handler{PublicKey: publicKey}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /notifications/vapid-public-key", h.get)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	key := ""
	if h != nil {
		key = h.PublicKey
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"public_key": key,
	})
}
