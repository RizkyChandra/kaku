// Package admin serves Kaku's authoring UI.
package admin

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/RizkyChandra/kaku/internal/auth"
	"github.com/RizkyChandra/kaku/internal/db"
	"github.com/RizkyChandra/kaku/internal/web/view"
)

type Handler struct {
	q    *db.Queries
	auth *auth.Service
}

func New(q *db.Queries, a *auth.Service) *Handler { return &Handler{q: q, auth: a} }

func (h *Handler) Router() chi.Router {
	r := chi.NewRouter()
	r.Use(auth.SameOriginPOST)

	r.Get("/login", h.loginForm)
	r.Post("/login", h.login)

	r.Group(func(r chi.Router) {
		r.Use(h.auth.RequireAuth)
		r.Get("/", h.dashboard)
		r.Post("/logout", h.logout)
	})
	return r
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	render(w, r, view.Dashboard(h.page(r, "Dashboard", "dashboard")))
}

func (h *Handler) loginForm(w http.ResponseWriter, r *http.Request) {
	render(w, r, view.Login(safeNext(r.URL.Query().Get("next")), ""))
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.FormValue("next"))
	_, err := h.auth.Login(r.Context(), w, strings.TrimSpace(r.FormValue("email")), r.FormValue("password"))
	if err != nil {
		if !errors.Is(err, auth.ErrInvalidCredentials) {
			slog.ErrorContext(r.Context(), "login", "err", err)
			renderStatus(w, r, http.StatusInternalServerError, view.Login(next, "Something went wrong. Try again."))
			return
		}
		renderStatus(w, r, http.StatusUnauthorized, view.Login(next, "Invalid email or password."))
		return
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if err := h.auth.Logout(r.Context(), w, r); err != nil {
		slog.ErrorContext(r.Context(), "logout", "err", err)
	}
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// page fills in the chrome every screen needs.
func (h *Handler) page(r *http.Request, title, active string) view.Page {
	p := view.Page{Title: title, Active: active}
	if u, ok := auth.UserFrom(r.Context()); ok {
		p.User = &u
	}
	return p
}

// safeNext keeps a post-login redirect on this site. "//host" and "/\host" are
// browser-relative but resolve off-origin, so only a single leading slash counts.
func safeNext(next string) string {
	if len(next) < 2 || next[0] != '/' || next[1] == '/' || next[1] == '\\' {
		return "/admin"
	}
	return next
}

// render writes a component, logging rather than panicking if the response is
// already half-written (nothing useful can be sent at that point).
func render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	renderStatus(w, r, http.StatusOK, c)
}

func renderStatus(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := c.Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "render", "path", r.URL.Path, "err", err)
	}
}
