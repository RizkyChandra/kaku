package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/RizkyChandra/kaku/internal/auth"
	"github.com/RizkyChandra/kaku/internal/content"
	"github.com/RizkyChandra/kaku/internal/db"
	"github.com/RizkyChandra/kaku/internal/web/view"
)

const (
	perPage      = 20
	maxRevisions = 25
	excerptRunes = 200
)

// Posts and pages differ only by the type column, so one editor and one set of
// /posts routes serve both; /pages is just the list with the filter flipped.
func (h *Handler) mountPosts(r chi.Router) {
	r.Get("/posts", h.listPosts("post"))
	r.Get("/pages", h.listPosts("page"))
	r.Get("/posts/new", h.newPost)
	r.Get("/posts/{id}", h.editPost)
	r.Post("/posts", h.createPost)
	r.Post("/posts/{id}", h.updatePost)
	r.Post("/posts/{id}/delete", h.deletePost)
	r.Post("/posts/{id}/restore/{revID}", h.restoreRevision)
	r.Post("/preview", h.preview)
}

func (h *Handler) listPosts(typ string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		status := listStatus(r.URL.Query().Get("status"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		offset := int64((page - 1) * perPage)

		var (
			total int64
			rows  []view.PostRow
			err   error
		)
		if status == "" {
			if total, err = h.q.CountPosts(ctx, typ); err != nil {
				h.fail(w, r, fmt.Errorf("count posts: %w", err))
				return
			}
			list, err := h.q.ListPosts(ctx, db.ListPostsParams{Type: typ, Limit: perPage, Offset: offset})
			if err != nil {
				h.fail(w, r, fmt.Errorf("list posts: %w", err))
				return
			}
			for _, p := range list {
				rows = append(rows, view.PostRow{
					ID: p.ID, Title: p.Title, Slug: p.Slug, Status: p.Status,
					Author: p.AuthorName, At: listedAt(p.PublishedAt, p.UpdatedAt),
				})
			}
		} else {
			if total, err = h.q.CountPostsByStatus(ctx, db.CountPostsByStatusParams{Type: typ, Status: status}); err != nil {
				h.fail(w, r, fmt.Errorf("count posts: %w", err))
				return
			}
			list, err := h.q.ListPostsByStatus(ctx, db.ListPostsByStatusParams{Type: typ, Status: status, Limit: perPage, Offset: offset})
			if err != nil {
				h.fail(w, r, fmt.Errorf("list posts: %w", err))
				return
			}
			for _, p := range list {
				rows = append(rows, view.PostRow{
					ID: p.ID, Title: p.Title, Slug: p.Slug, Status: p.Status,
					Author: p.AuthorName, At: listedAt(p.PublishedAt, p.UpdatedAt),
				})
			}
		}

		title := "Posts"
		if typ == "page" {
			title = "Pages"
		}
		render(w, r, view.Posts(h.page(r, title, navKey(typ)), view.PostList{
			Type:   typ,
			Status: status,
			Page:   page,
			Pages:  int((total + perPage - 1) / perPage),
			Rows:   rows,
		}))
	}
}

func (h *Handler) newPost(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	typ := postType(r.URL.Query().Get("type"))
	vis := postVisibility(db.LoadSettings(r.Context(), h.q).Get("default_visibility"))
	render(w, r, view.Editor(h.page(r, "New "+typ, navKey(typ)), view.EditorData{
		Post:       db.Post{Type: typ, Status: "draft", Visibility: vis},
		CanPublish: canPublish(u),
	}))
}

func (h *Handler) editPost(w http.ResponseWriter, r *http.Request) {
	p, u, ok := h.editable(w, r)
	if !ok {
		return
	}
	e, err := h.editorData(r.Context(), p, u)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	render(w, r, view.Editor(h.page(r, p.Title, navKey(p.Type)), e))
}

func (h *Handler) createPost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, _ := auth.UserFrom(ctx)
	typ := postType(r.FormValue("type"))
	f := readPostForm(r)

	if f.Status != "draft" && !canPublish(u) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if msg := f.validate(); msg != "" {
		h.editorError(w, r, db.Post{Type: typ}, f, u, msg)
		return
	}
	slug, err := content.UniqueSlug(ctx, f.slugBase(), h.q.PostSlugExists)
	if err != nil {
		h.fail(w, r, fmt.Errorf("unique slug: %w", err))
		return
	}
	p, err := h.q.CreatePost(ctx, db.CreatePostParams{
		Uuid:         uuid.NewString(),
		Type:         typ,
		Title:        f.Title,
		Slug:         slug,
		Markdown:     f.Markdown,
		Html:         content.Render(f.Markdown),
		Excerpt:      f.excerpt(),
		FeatureImage: f.FeatureImage,
		Status:       f.Status,
		Visibility:   f.Visibility,
		AuthorID:     u.ID,
		PublishedAt:  f.publishedAt(nil),
	})
	if err != nil {
		h.fail(w, r, fmt.Errorf("create post: %w", err))
		return
	}
	if err := h.syncTags(ctx, p.ID, f.Tags); err != nil {
		h.fail(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/posts/%d", p.ID), http.StatusSeeOther)
}

func (h *Handler) updatePost(w http.ResponseWriter, r *http.Request) {
	p, u, ok := h.editable(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	f := readPostForm(r)

	if f.Status != "draft" && !canPublish(u) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if msg := f.validate(); msg != "" {
		h.editorError(w, r, p, f, u, msg)
		return
	}
	slug, err := content.UniqueSlug(ctx, f.slugBase(), func(ctx context.Context, s string) (bool, error) {
		return h.q.PostSlugExistsExcept(ctx, db.PostSlugExistsExceptParams{Slug: s, ID: p.ID})
	})
	if err != nil {
		h.fail(w, r, fmt.Errorf("unique slug: %w", err))
		return
	}

	h.snapshot(ctx, p, u)
	if _, err := h.q.UpdatePost(ctx, db.UpdatePostParams{
		Title:        f.Title,
		Slug:         slug,
		Markdown:     f.Markdown,
		Html:         content.Render(f.Markdown),
		Excerpt:      f.excerpt(),
		FeatureImage: f.FeatureImage,
		Status:       f.Status,
		Visibility:   f.Visibility,
		PublishedAt:  f.publishedAt(p.PublishedAt),
		ID:           p.ID,
	}); err != nil {
		h.fail(w, r, fmt.Errorf("update post: %w", err))
		return
	}
	if err := h.syncTags(ctx, p.ID, f.Tags); err != nil {
		h.fail(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/posts/%d", p.ID), http.StatusSeeOther)
}

// restoreRevision puts an old revision's text back, keeping everything else
// (slug, status, tags) as it is now. The current text becomes a revision first,
// so restoring is itself undoable.
func (h *Handler) restoreRevision(w http.ResponseWriter, r *http.Request) {
	p, u, ok := h.editable(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "revID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rev, err := h.q.GetRevision(ctx, id)
	if err != nil || rev.PostID != p.ID {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			h.fail(w, r, fmt.Errorf("get revision: %w", err))
			return
		}
		http.NotFound(w, r)
		return
	}

	h.snapshot(ctx, p, u)
	if _, err := h.q.UpdatePost(ctx, db.UpdatePostParams{
		Title:        rev.Title,
		Slug:         p.Slug,
		Markdown:     rev.Markdown,
		Html:         content.Render(rev.Markdown),
		Excerpt:      p.Excerpt,
		FeatureImage: p.FeatureImage,
		Status:       p.Status,
		Visibility:   p.Visibility,
		PublishedAt:  stamp(p.PublishedAt),
		ID:           p.ID,
	}); err != nil {
		h.fail(w, r, fmt.Errorf("restore revision: %w", err))
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/posts/%d", p.ID), http.StatusSeeOther)
}

func (h *Handler) deletePost(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.editable(w, r)
	if !ok {
		return
	}
	if err := h.q.DeletePost(r.Context(), p.ID); err != nil {
		h.fail(w, r, fmt.Errorf("delete post: %w", err))
		return
	}
	http.Redirect(w, r, "/admin/"+navKey(p.Type), http.StatusSeeOther)
}

// preview renders the editor's markdown as a fragment for the live pane. It
// goes through content.Render, which sanitises, so the author's own input can
// never execute here either.
func (h *Handler) preview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, content.Render(r.FormValue("markdown")))
}

// editable loads the post named by {id} and checks the signed-in user may
// change it, writing the response itself when they may not.
func (h *Handler) editable(w http.ResponseWriter, r *http.Request) (db.Post, db.User, bool) {
	u, _ := auth.UserFrom(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return db.Post{}, u, false
	}
	p, err := h.q.GetPost(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			h.fail(w, r, fmt.Errorf("get post: %w", err))
		}
		return db.Post{}, u, false
	}
	if !canEdit(u, p) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return db.Post{}, u, false
	}
	return p, u, true
}

// canEdit: editors and up may touch anything, authors only their own posts,
// contributors only their own drafts.
func canEdit(u db.User, p db.Post) bool {
	switch u.Role {
	case auth.RoleOwner, auth.RoleAdmin, auth.RoleEditor:
		return true
	case auth.RoleAuthor:
		return p.AuthorID == u.ID
	case auth.RoleContributor:
		return p.AuthorID == u.ID && p.Status == "draft"
	}
	return false
}

func canPublish(u db.User) bool { return u.Role != auth.RoleContributor }

// snapshot keeps the pre-edit text, newest maxRevisions only. History is a
// convenience: losing it must never lose the edit, so failures only log.
func (h *Handler) snapshot(ctx context.Context, p db.Post, u db.User) {
	if err := h.q.CreateRevision(ctx, db.CreateRevisionParams{
		PostID: p.ID, Title: p.Title, Markdown: p.Markdown, AuthorID: u.ID,
	}); err != nil {
		slog.ErrorContext(ctx, "create revision", "post", p.ID, "err", err)
		return
	}
	revs, err := h.q.ListRevisions(ctx, db.ListRevisionsParams{PostID: p.ID, Limit: maxRevisions})
	if err == nil && len(revs) == maxRevisions {
		err = h.q.DeleteRevisionsBelowID(ctx, db.DeleteRevisionsBelowIDParams{
			PostID: p.ID, ID: revs[len(revs)-1].ID,
		})
	}
	if err != nil {
		slog.ErrorContext(ctx, "prune revisions", "post", p.ID, "err", err)
	}
}

// syncTags replaces the post's tags with the names typed in the form, creating
// the ones that do not exist yet. Order is the order they were typed in.
func (h *Handler) syncTags(ctx context.Context, postID int64, names string) error {
	if err := h.q.ClearPostTags(ctx, postID); err != nil {
		return fmt.Errorf("clear post tags: %w", err)
	}
	for i, name := range strings.Split(names, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		slug := content.Slugify(name)
		t, err := h.q.GetTagBySlug(ctx, slug)
		if errors.Is(err, sql.ErrNoRows) {
			t, err = h.q.CreateTag(ctx, db.CreateTagParams{Name: name, Slug: slug})
		}
		if err != nil {
			return fmt.Errorf("tag %q: %w", name, err)
		}
		if err := h.q.AddPostTag(ctx, db.AddPostTagParams{PostID: postID, TagID: t.ID, Position: int64(i)}); err != nil {
			return fmt.Errorf("tag %q: %w", name, err)
		}
	}
	return nil
}

func (h *Handler) editorData(ctx context.Context, p db.Post, u db.User) (view.EditorData, error) {
	tags, err := h.q.ListPostTags(ctx, p.ID)
	if err != nil {
		return view.EditorData{}, fmt.Errorf("list post tags: %w", err)
	}
	names := make([]string, len(tags))
	for i, t := range tags {
		names[i] = t.Name
	}
	revs, err := h.q.ListRevisions(ctx, db.ListRevisionsParams{PostID: p.ID, Limit: maxRevisions})
	if err != nil {
		return view.EditorData{}, fmt.Errorf("list revisions: %w", err)
	}
	return view.EditorData{
		Post:       p,
		Tags:       strings.Join(names, ", "),
		Revisions:  revs,
		CanPublish: canPublish(u),
		Schedule:   scheduleValue(p.PublishedAt),
	}, nil
}

// editorError re-renders the form with what the author typed still in it —
// a rejected save must never cost them their draft.
func (h *Handler) editorError(w http.ResponseWriter, r *http.Request, p db.Post, f postForm, u db.User, msg string) {
	p.Title, p.Slug, p.Markdown = f.Title, f.Slug, f.Markdown
	p.Excerpt, p.FeatureImage, p.Status = f.Excerpt, f.FeatureImage, f.Status
	p.Visibility = f.Visibility
	p.Html = content.Render(f.Markdown)
	title := p.Title
	if title == "" {
		title = "New " + p.Type
	}
	renderStatus(w, r, http.StatusBadRequest, view.Editor(h.page(r, title, navKey(p.Type)), view.EditorData{
		Post:       p,
		Tags:       f.Tags,
		CanPublish: canPublish(u),
		Schedule:   f.Schedule,
		Err:        msg,
	}))
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "posts", "path", r.URL.Path, "err", err)
	http.Error(w, "something went wrong", http.StatusInternalServerError)
}

type postForm struct {
	Title, Slug, Markdown, Excerpt, FeatureImage, Tags, Status, Schedule string
	Visibility                                                           string
}

func readPostForm(r *http.Request) postForm {
	return postForm{
		Title:        strings.TrimSpace(r.FormValue("title")),
		Slug:         strings.TrimSpace(r.FormValue("slug")),
		Markdown:     r.FormValue("markdown"),
		Excerpt:      strings.TrimSpace(r.FormValue("excerpt")),
		FeatureImage: strings.TrimSpace(r.FormValue("feature_image")),
		Tags:         r.FormValue("tags"),
		Status:       postStatus(r.FormValue("status")),
		Schedule:     r.FormValue("published_at"),
		Visibility:   postVisibility(r.FormValue("visibility")),
	}
}

func (f postForm) validate() string {
	if f.Title == "" {
		return "Give it a title before saving."
	}
	if f.Status == "scheduled" {
		if _, err := parseSchedule(f.Schedule); err != nil {
			return "A scheduled post needs a publish date."
		}
	}
	return ""
}

// slugBase slugifies whatever the author typed — a hand-written slug is still
// normalised, never trusted raw — and falls back to the title.
func (f postForm) slugBase() string {
	if f.Slug != "" {
		return content.Slugify(f.Slug)
	}
	return content.Slugify(f.Title)
}

func (f postForm) excerpt() string {
	if f.Excerpt != "" {
		return f.Excerpt
	}
	return content.Excerpt(f.Markdown, excerptRunes)
}

// publishedAt is what Create/UpdatePost want for published_at: nil or RFC3339
// UTC text, never a time.Time. cur is the value the post already has.
func (f postForm) publishedAt(cur *time.Time) interface{} {
	now := time.Now().UTC()
	switch f.Status {
	case "published":
		// Keep the original date on re-saves, but a post scheduled for the
		// future that is published by hand goes out now.
		if cur != nil && !cur.After(now) {
			return stamp(cur)
		}
		return now.Format(time.RFC3339)
	case "scheduled":
		t, err := parseSchedule(f.Schedule)
		if err != nil {
			return nil // unreachable: validate rejects this first
		}
		return t.Format(time.RFC3339)
	}
	return nil
}

// parseSchedule reads a datetime-local field. The browser sends bare wall-clock
// text with no zone, so it is read as UTC: the schedule an author types is the
// UTC instant the post goes out, matching what the API and feeds serve.
func parseSchedule(v string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("publish date %q: want YYYY-MM-DDTHH:MM", v)
}

func stamp(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// scheduleValue fills the datetime-local input, in UTC to match parseSchedule.
func scheduleValue(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04")
}

func postStatus(s string) string {
	switch s {
	case "scheduled", "published":
		return s
	}
	return "draft"
}

// postVisibility keeps the column's CHECK constraint out of the error path: an
// unknown value from a hand-crafted form falls back to the safer of the two.
func postVisibility(v string) string {
	if v == "private" {
		return "private"
	}
	return "public"
}

// listStatus is the ?status= filter: "" means every status.
func listStatus(s string) string {
	switch s {
	case "draft", "scheduled", "published":
		return s
	}
	return ""
}

func postType(v string) string {
	if v == "page" {
		return "page"
	}
	return "post"
}

func navKey(typ string) string { return typ + "s" }

func listedAt(published *time.Time, updated time.Time) time.Time {
	if published != nil {
		return *published
	}
	return updated
}
