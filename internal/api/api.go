// Package api serves Kaku's read-only Content API.
package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/RizkyChandra/kaku/internal/db"
)

const (
	authScheme   = "Kaku "
	defaultLimit = 10
	maxLimit     = 100
	// The tag list is not paginated in the API; a site with more tags than this
	// wants a paginated endpoint, not a bigger number.
	maxTags = 500
)

type Handler struct{ q *db.Queries }

func New(q *db.Queries) *Handler { return &Handler{q: q} }

func (h *Handler) Router() chi.Router {
	r := chi.NewRouter()
	r.Use(cors, h.requireKey)

	r.Get("/posts", h.listPosts)
	r.Get("/posts/{slug}", h.getPost)
	r.Get("/pages", h.listPages)
	r.Get("/pages/{slug}", h.getPage)
	r.Get("/tags", h.listTags)

	// chi's defaults are plain text; this API only ever speaks JSON.
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, r, http.StatusNotFound, "not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, r, http.StatusMethodNotAllowed, "method not allowed")
	})
	return r
}

// HashKey is the stored form of a Content API key. The admin screen hashes with
// this too, so the two sides cannot drift apart.
func HashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// The API is read-only and key-gated, so any origin may call it.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) requireKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := strings.CutPrefix(r.Header.Get("Authorization"), authScheme)
		if !ok || key == "" {
			writeErr(w, r, http.StatusUnauthorized, "missing or malformed Authorization header")
			return
		}
		hash := HashKey(key)
		k, err := h.q.GetApiKeyByHash(r.Context(), hash)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				slog.ErrorContext(r.Context(), "api: lookup key", "err", err)
			}
			writeErr(w, r, http.StatusUnauthorized, "invalid api key")
			return
		}
		if subtle.ConstantTimeCompare([]byte(k.KeyHash), []byte(hash)) != 1 {
			writeErr(w, r, http.StatusUnauthorized, "invalid api key")
			return
		}
		if err := h.q.TouchApiKey(r.Context(), k.ID); err != nil {
			slog.ErrorContext(r.Context(), "api: touch key", "key", k.ID, "err", err)
		}
		next.ServeHTTP(w, r)
	})
}

// Explicit response types: the contract must not change when a column is added,
// and markdown, ids and status must never reach a reader.
type postJSON struct {
	UUID         string     `json:"uuid"`
	Title        string     `json:"title"`
	Slug         string     `json:"slug"`
	HTML         string     `json:"html"`
	Excerpt      string     `json:"excerpt"`
	FeatureImage string     `json:"feature_image"`
	PublishedAt  *time.Time `json:"published_at"`
	Author       string     `json:"author"`
	Tags         []tagJSON  `json:"tags"`
}

type tagJSON struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
}

type meta struct {
	Page  int64 `json:"page"`
	Limit int64 `json:"limit"`
	Total int64 `json:"total"`
	Pages int64 `json:"pages"`
}

func newMeta(page, limit, total int64) meta {
	return meta{Page: page, Limit: limit, Total: total, Pages: (total + limit - 1) / limit}
}

func (h *Handler) listPosts(w http.ResponseWriter, r *http.Request) {
	page, limit := h.paging(r)
	tag := r.URL.Query().Get("tag")
	if tag == "" {
		h.listByType(w, r, "post", "posts", page, limit)
		return
	}
	rows, err := h.q.ListPublishedPostsByTag(r.Context(), db.ListPublishedPostsByTagParams{
		Slug: tag, Limit: limit, Offset: (page - 1) * limit,
	})
	if err != nil {
		fail(w, r, "list posts by tag", err)
		return
	}
	// The published-post queries generate identical row structs, so converting
	// lets one mapper serve all three.
	posts := make([]db.ListPublishedPostsRow, len(rows))
	for i, row := range rows {
		posts[i] = db.ListPublishedPostsRow(row)
	}
	total, err := h.q.CountPublishedPostsByTag(r.Context(), tag)
	if err != nil {
		fail(w, r, "count posts by tag", err)
		return
	}
	h.writePosts(w, r, "posts", posts, newMeta(page, limit, total))
}

func (h *Handler) listPages(w http.ResponseWriter, r *http.Request) {
	page, limit := h.paging(r)
	h.listByType(w, r, "page", "pages", page, limit)
}

func (h *Handler) listByType(w http.ResponseWriter, r *http.Request, typ, key string, page, limit int64) {
	rows, err := h.q.ListPublishedPosts(r.Context(), db.ListPublishedPostsParams{
		Type: typ, Limit: limit, Offset: (page - 1) * limit,
	})
	if err != nil {
		fail(w, r, "list published posts", err)
		return
	}
	total, err := h.q.CountPublishedPosts(r.Context(), typ)
	if err != nil {
		fail(w, r, "count published posts", err)
		return
	}
	h.writePosts(w, r, key, rows, newMeta(page, limit, total))
}

func (h *Handler) getPost(w http.ResponseWriter, r *http.Request) { h.bySlug(w, r, "post") }
func (h *Handler) getPage(w http.ResponseWriter, r *http.Request) { h.bySlug(w, r, "page") }

// bySlug also checks the type, since the slug lookup spans posts and pages.
func (h *Handler) bySlug(w http.ResponseWriter, r *http.Request, typ string) {
	row, err := h.q.GetPublishedPostBySlug(r.Context(), chi.URLParam(r, "slug"))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		fail(w, r, "get published post", err)
		return
	}
	if err != nil || row.Type != typ {
		writeErr(w, r, http.StatusNotFound, "not found")
		return
	}
	p, err := h.post(r.Context(), db.ListPublishedPostsRow(row))
	if err != nil {
		fail(w, r, "list post tags", err)
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{typ: p})
}

func (h *Handler) listTags(w http.ResponseWriter, r *http.Request) {
	rows, err := h.q.ListTags(r.Context(), db.ListTagsParams{Limit: maxTags, Offset: 0})
	if err != nil {
		fail(w, r, "list tags", err)
		return
	}
	tags := make([]tagJSON, 0, len(rows))
	for _, t := range rows {
		tags = append(tags, tagJSON{Name: t.Name, Slug: t.Slug, Description: t.Description})
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"tags": tags})
}

func (h *Handler) writePosts(w http.ResponseWriter, r *http.Request, key string, rows []db.ListPublishedPostsRow, m meta) {
	posts := make([]postJSON, 0, len(rows))
	for _, row := range rows {
		p, err := h.post(r.Context(), row)
		if err != nil {
			fail(w, r, "list post tags", err)
			return
		}
		posts = append(posts, p)
	}
	writeJSON(w, r, http.StatusOK, map[string]any{key: posts, "meta": m})
}

func (h *Handler) post(ctx context.Context, row db.ListPublishedPostsRow) (postJSON, error) {
	// ponytail: one tag query per post. A page is at most 100 rows; join them in
	// SQL if this ever shows up in a profile.
	tags, err := h.q.ListPostTags(ctx, row.ID)
	if err != nil {
		return postJSON{}, err
	}
	p := postJSON{
		UUID:         row.Uuid,
		Title:        row.Title,
		Slug:         row.Slug,
		HTML:         row.Html,
		Excerpt:      row.Excerpt,
		FeatureImage: row.FeatureImage,
		PublishedAt:  row.PublishedAt,
		Author:       row.AuthorName,
		Tags:         make([]tagJSON, 0, len(tags)),
	}
	for _, t := range tags {
		p.Tags = append(p.Tags, tagJSON{Name: t.Name, Slug: t.Slug, Description: t.Description})
	}
	return p, nil
}

// paging reads page and limit, falling back to the defaults on anything unusable.
// paging honours ?limit= when given, otherwise the posts_per_page setting.
// A client asking for more than maxLimit is capped rather than refused.
func (h *Handler) paging(r *http.Request) (page, limit int64) {
	q := r.URL.Query()
	def := db.LoadSettings(r.Context(), h.q).Int("posts_per_page", defaultLimit, 1, maxLimit)
	return posInt(q.Get("page"), 1), min(posInt(q.Get("limit"), def), maxLimit)
}

func posInt(s string, def int64) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 1 {
		return def
	}
	return n
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.ErrorContext(r.Context(), "api: encode", "path", r.URL.Path, "err", err)
	}
}

func writeErr(w http.ResponseWriter, r *http.Request, status int, msg string) {
	writeJSON(w, r, status, map[string]string{"error": msg})
}

// fail logs the real error and tells the caller nothing about it.
func fail(w http.ResponseWriter, r *http.Request, what string, err error) {
	slog.ErrorContext(r.Context(), "api: "+what, "path", r.URL.Path, "err", err)
	writeErr(w, r, http.StatusInternalServerError, "internal error")
}
