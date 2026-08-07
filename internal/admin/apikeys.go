package admin

import (
	"crypto/rand"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/RizkyChandra/kaku/internal/api"
	"github.com/RizkyChandra/kaku/internal/auth"
	"github.com/RizkyChandra/kaku/internal/db"
	"github.com/RizkyChandra/kaku/internal/web/view"
)

func (h *Handler) mountAPIKeys(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireRole(auth.RoleOwner, auth.RoleAdmin))
		r.Get("/keys", h.apiKeys)
		r.Post("/keys", h.createAPIKey)
		r.Post("/keys/{id}/delete", h.deleteAPIKey)
	})
}

func (h *Handler) apiKeys(w http.ResponseWriter, r *http.Request) {
	h.renderAPIKeys(w, r, http.StatusOK, "", "")
}

func (h *Handler) createAPIKey(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		h.renderAPIKeys(w, r, http.StatusBadRequest, "", "Give the key a name.")
		return
	}
	u, _ := auth.UserFrom(r.Context())
	// rand.Text is 26 base32 characters of crypto/rand entropy.
	key := rand.Text()
	if _, err := h.q.CreateApiKey(r.Context(), db.CreateApiKeyParams{
		Name: name, KeyHash: api.HashKey(key), CreatedBy: u.ID,
	}); err != nil {
		slog.ErrorContext(r.Context(), "create api key", "err", err)
		h.renderAPIKeys(w, r, http.StatusInternalServerError, "", "Could not create the key. Try again.")
		return
	}
	// The only time the plaintext exists anywhere: only its hash was stored.
	h.renderAPIKeys(w, r, http.StatusOK, key, "")
}

func (h *Handler) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad key id", http.StatusBadRequest)
		return
	}
	if err := h.q.DeleteApiKey(r.Context(), id); err != nil {
		slog.ErrorContext(r.Context(), "delete api key", "key", id, "err", err)
		http.Error(w, "could not revoke the key", http.StatusInternalServerError)
		return
	}
	keys, err := h.q.ListApiKeys(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "list api keys", "err", err)
		http.Error(w, "could not load keys", http.StatusInternalServerError)
		return
	}
	render(w, r, view.APIKeyTable(keys)) // htmx swaps the table in place
}

func (h *Handler) renderAPIKeys(w http.ResponseWriter, r *http.Request, status int, created, errMsg string) {
	keys, err := h.q.ListApiKeys(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "list api keys", "err", err)
		http.Error(w, "could not load keys", http.StatusInternalServerError)
		return
	}
	renderStatus(w, r, status, view.APIKeys(h.page(r, "Content API keys", "keys"), keys, created, errMsg))
}
