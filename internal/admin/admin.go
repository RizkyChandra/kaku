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
	"github.com/RizkyChandra/kaku/internal/backup"
	"github.com/RizkyChandra/kaku/internal/config"
	"github.com/RizkyChandra/kaku/internal/db"
	"github.com/RizkyChandra/kaku/internal/media"
	"github.com/RizkyChandra/kaku/internal/web/view"
)

type Handler struct {
	q    *db.Queries
	auth *auth.Service
	// nil when no S3 bucket is configured; upload handlers must say so rather
	// than panic, since Kaku is usable without media.
	media *media.Store
	// nil when no bucket is configured; the backup handler says so rather than
	// pretending it worked.
	backups *backup.Backup
	cfg     config.Config
	limiter *auth.Limiter
}

func New(q *db.Queries, a *auth.Service, m *media.Store, b *backup.Backup, cfg config.Config) *Handler {
	return &Handler{q: q, auth: a, media: m, backups: b, cfg: cfg, limiter: auth.NewLimiter()}
}

func (h *Handler) Router() chi.Router {
	r := chi.NewRouter()
	r.Use(auth.SameOriginPOST)

	r.Get("/login", h.loginForm)
	r.Post("/login", h.login)

	r.Group(func(r chi.Router) {
		r.Use(h.auth.RequireAuth)
		r.Get("/", h.dashboard)
		r.Post("/logout", h.logout)

		// Each feature registers its own routes from its own file.
		h.mountPosts(r)
		h.mountSearch(r)
		h.mountTags(r)
		h.mountMedia(r)
		h.mountUsers(r)
		h.mountSettings(r)
		h.mountAPIKeys(r)
	})
	return r
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := view.DashboardData{}

	// Counts are cheap on SQLite and a dashboard that silently hides a failed
	// count is worse than one showing zero, so a failure is logged and the
	// screen still renders.
	count := func(name string, f func() (int64, error)) int64 {
		n, err := f()
		if err != nil {
			slog.ErrorContext(ctx, "dashboard count", "which", name, "err", err)
		}
		return n
	}

	posts := count("posts", func() (int64, error) { return h.q.CountPosts(ctx, "post") })
	pages := count("pages", func() (int64, error) { return h.q.CountPosts(ctx, "page") })
	d.Drafts = count("drafts", func() (int64, error) {
		return h.q.CountPostsByStatus(ctx, db.CountPostsByStatusParams{Type: "post", Status: "draft"})
	})
	d.Scheduled = count("scheduled", func() (int64, error) {
		return h.q.CountPostsByStatus(ctx, db.CountPostsByStatusParams{Type: "post", Status: "scheduled"})
	})
	d.Stats = []view.Stat{
		{Label: "Posts", Count: posts, Href: "/admin/posts"},
		{Label: "Pages", Count: pages, Href: "/admin/pages"},
		{Label: "Drafts", Count: d.Drafts, Href: "/admin/posts?status=draft"},
		{Label: "Tags", Count: count("tags", func() (int64, error) { return h.q.CountTags(ctx) }), Href: "/admin/tags"},
		{Label: "Media", Count: count("media", func() (int64, error) { return h.q.CountMedia(ctx) }), Href: "/admin/media"},
		{Label: "Staff", Count: count("users", func() (int64, error) { return h.q.CountUsers(ctx) }), Href: "/admin/users"},
	}

	if rows, err := h.q.ListPosts(ctx, db.ListPostsParams{Type: "post", Limit: 5, Offset: 0}); err != nil {
		slog.ErrorContext(ctx, "dashboard recent posts", "err", err)
	} else {
		for _, row := range rows {
			d.Recent = append(d.Recent, view.PostRow{
				ID: row.ID, Title: row.Title, Slug: row.Slug,
				Status: row.Status, Author: row.AuthorName,
			})
		}
	}

	render(w, r, view.Dashboard(h.page(r, "Dashboard", "dashboard"), d))
}

func (h *Handler) loginForm(w http.ResponseWriter, r *http.Request) {
	render(w, r, view.Login(safeNext(r.URL.Query().Get("next")), ""))
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.FormValue("next"))
	email := strings.TrimSpace(r.FormValue("email"))
	ip := auth.ClientIP(r)

	// A throttled attempt must be indistinguishable from a wrong password, or
	// the limiter becomes an account-existence oracle.
	if !h.limiter.Allow(ip, email) {
		slog.WarnContext(r.Context(), "login throttled", "ip", ip)
		renderStatus(w, r, http.StatusUnauthorized, view.Login(next, "Invalid email or password."))
		return
	}

	_, err := h.auth.Login(r.Context(), w, email, r.FormValue("password"))
	if err != nil {
		h.limiter.Fail(ip, email)
		if !errors.Is(err, auth.ErrInvalidCredentials) {
			slog.ErrorContext(r.Context(), "login", "err", err)
			renderStatus(w, r, http.StatusInternalServerError, view.Login(next, "Something went wrong. Try again."))
			return
		}
		renderStatus(w, r, http.StatusUnauthorized, view.Login(next, "Invalid email or password."))
		return
	}
	h.limiter.Succeed(email)
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
	s := db.LoadSettings(r.Context(), h.q)
	p := view.Page{
		Title:     title,
		Active:    active,
		SiteTitle: s.Get("site_title"),
		Footer:    s.Get("footer_text"),
	}
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
