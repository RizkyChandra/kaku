package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/RizkyChandra/kaku/internal/auth"
	"github.com/RizkyChandra/kaku/internal/content"
	"github.com/RizkyChandra/kaku/internal/db"
	"github.com/RizkyChandra/kaku/internal/i18n"
	"github.com/RizkyChandra/kaku/internal/web/view"
)

func (h *Handler) mountTags(r chi.Router) {
	r.Get("/tags", h.tagList)

	r.Group(func(r chi.Router) {
		// Tags are site taxonomy: authors and contributors write posts, they do
		// not curate the vocabulary.
		r.Use(auth.RequireRole(auth.RoleEditor, auth.RoleAdmin, auth.RoleOwner))
		r.Post("/tags", h.tagCreate)
		r.Get("/tags/{id}", h.tagRow)       // htmx fragment: cancel an edit
		r.Get("/tags/{id}/edit", h.tagEdit) // htmx fragment: the edit form
		r.Post("/tags/{id}", h.tagUpdate)
		r.Post("/tags/{id}/delete", h.tagDelete)
	})
}

func (h *Handler) tagList(w http.ResponseWriter, r *http.Request) {
	h.tagPage(w, r, http.StatusOK, "")
}

func (h *Handler) tagCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		h.tagPage(w, r, http.StatusBadRequest, i18n.T(r.Context(), "tags.errNameRequired"))
		return
	}
	slug, err := h.tagUniqueSlug(r.Context(), name, r.FormValue("slug"), 0)
	if err != nil {
		tagFail(w, r, err)
		return
	}
	if _, err := h.q.CreateTag(r.Context(), db.CreateTagParams{
		Name:        name,
		Slug:        slug,
		Description: strings.TrimSpace(r.FormValue("description")),
	}); err != nil {
		tagFail(w, r, fmt.Errorf("create tag: %w", err))
		return
	}
	http.Redirect(w, r, "/admin/tags", http.StatusSeeOther)
}

func (h *Handler) tagRow(w http.ResponseWriter, r *http.Request) {
	t, ok := h.tagLookup(w, r)
	if !ok {
		return
	}
	render(w, r, view.TagRow(t))
}

func (h *Handler) tagEdit(w http.ResponseWriter, r *http.Request) {
	t, ok := h.tagLookup(w, r)
	if !ok {
		return
	}
	render(w, r, view.TagEditRow(t, ""))
}

func (h *Handler) tagUpdate(w http.ResponseWriter, r *http.Request) {
	t, ok := h.tagLookup(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		renderStatus(w, r, http.StatusBadRequest, view.TagEditRow(t, i18n.T(r.Context(), "tags.errNameRequired")))
		return
	}
	slug, err := h.tagUniqueSlug(r.Context(), name, r.FormValue("slug"), t.ID)
	if err != nil {
		tagFail(w, r, err)
		return
	}
	updated, err := h.q.UpdateTag(r.Context(), db.UpdateTagParams{
		Name:        name,
		Slug:        slug,
		Description: strings.TrimSpace(r.FormValue("description")),
		ID:          t.ID,
	})
	if err != nil {
		tagFail(w, r, fmt.Errorf("update tag %d: %w", t.ID, err))
		return
	}
	t.Name, t.Slug, t.Description = updated.Name, updated.Slug, updated.Description
	render(w, r, view.TagRow(t))
}

func (h *Handler) tagDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// post_tags.tag_id is ON DELETE CASCADE, so this detaches the tag from every
	// post on its own.
	if err := h.q.DeleteTag(r.Context(), id); err != nil {
		tagFail(w, r, fmt.Errorf("delete tag %d: %w", id, err))
		return
	}
	// Empty body: htmx swaps the row away.
}

func (h *Handler) tagPage(w http.ResponseWriter, r *http.Request, status int, errMsg string) {
	total, err := h.q.CountTags(r.Context())
	if err != nil {
		tagFail(w, r, fmt.Errorf("count tags: %w", err))
		return
	}
	page, pages := pageBounds(r, total)
	tags, err := h.q.ListTags(r.Context(), db.ListTagsParams{Limit: perPage, Offset: int64((page - 1) * perPage)})
	if err != nil {
		tagFail(w, r, fmt.Errorf("list tags: %w", err))
		return
	}
	renderStatus(w, r, status, view.Tags(h.page(r, i18n.T(r.Context(), "tags.title"), "tags"), tags, page, pages, errMsg))
}

// tagLookup resolves the {id} route param, answering 404 itself when it cannot.
//
// GetTag is the narrowest query that does it. Scanning ListTags used to work
// only because it returned every tag; now that it is a page at a time, a tag on
// page two would read as missing. GetTag has every column the row fragments
// need except post_count, which no single-tag query carries and which nothing
// here changes — so the row hands its count back in ?posts= rather than paying
// for a count on every edit, cancel and save.
func (h *Handler) tagLookup(w http.ResponseWriter, r *http.Request) (db.ListTagsRow, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return db.ListTagsRow{}, false
	}
	t, err := h.q.GetTag(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			tagFail(w, r, fmt.Errorf("get tag %d: %w", id, err))
		}
		return db.ListTagsRow{}, false
	}
	posts, _ := strconv.ParseInt(r.FormValue("posts"), 10, 64)
	return db.ListTagsRow{
		ID: t.ID, Name: t.Name, Slug: t.Slug, Description: t.Description,
		CreatedAt: t.CreatedAt, PostCount: posts,
	}, true
}

// tagUniqueSlug slugifies the submitted slug, or the name when it is blank, and
// bumps it until it is free. exceptID is 0 when creating.
func (h *Handler) tagUniqueSlug(ctx context.Context, name, slug string, exceptID int64) (string, error) {
	base := content.Slugify(name)
	if s := strings.TrimSpace(slug); s != "" {
		base = content.Slugify(s)
	}
	exists := h.q.TagSlugExists
	if exceptID != 0 {
		exists = func(ctx context.Context, s string) (bool, error) {
			return h.q.TagSlugExistsExcept(ctx, db.TagSlugExistsExceptParams{Slug: s, ID: exceptID})
		}
	}
	s, err := content.UniqueSlug(ctx, base, exists)
	if err != nil {
		return "", fmt.Errorf("tag slug: %w", err)
	}
	return s, nil
}

func tagFail(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "tags", "path", r.URL.Path, "err", err)
	http.Error(w, "something went wrong", http.StatusInternalServerError)
}
