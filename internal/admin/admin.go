// Package admin serves Kaku's authoring UI.
package admin

import (
	"log/slog"
	"net/http"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/RizkyChandra/kaku/internal/db"
	"github.com/RizkyChandra/kaku/internal/web/view"
)

type Handler struct {
	q *db.Queries
}

func New(q *db.Queries) *Handler { return &Handler{q: q} }

func (h *Handler) Router() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.dashboard)
	return r
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	render(w, r, view.Dashboard(view.Page{Title: "Dashboard", Active: "dashboard"}))
}

// render writes a component, logging rather than panicking if the response is
// already half-written (nothing useful can be sent at that point).
func render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "render", "path", r.URL.Path, "err", err)
	}
}
