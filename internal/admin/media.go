package admin

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/RizkyChandra/kaku/internal/auth"
	"github.com/RizkyChandra/kaku/internal/db"
	"github.com/RizkyChandra/kaku/internal/media"
	"github.com/RizkyChandra/kaku/internal/web/view"
)

const mediaPerPage = 24

// A description long enough to be useful and short enough to still be alt
// text; past this it is prose that belongs in the post.
const mediaAltMax = 300

// Anyone signed in may upload; deleting breaks other people's posts, so it
// stops at editor.
var mediaDeleteRoles = []string{auth.RoleEditor, auth.RoleAdmin, auth.RoleOwner}

func (h *Handler) mountMedia(r chi.Router) {
	r.Route("/media", func(r chi.Router) {
		r.Get("/", h.mediaIndex)
		r.Post("/", h.mediaUpload)
		// No RequireRole: captioning destroys nothing, and an image nobody is
		// allowed to describe stays inaccessible. Same reach as upload.
		r.Post("/{id}/alt", h.mediaAlt)
		r.With(auth.RequireRole(mediaDeleteRoles...)).Post("/{id}/delete", h.mediaDelete)
	})
}

func (h *Handler) mediaIndex(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	items, err := h.q.ListMedia(r.Context(), db.ListMediaParams{
		Limit:  mediaPerPage,
		Offset: int64(page-1) * mediaPerPage,
	})
	if err != nil {
		mediaFail(w, r, "list media", err)
		return
	}
	total, err := h.q.CountMedia(r.Context())
	if err != nil {
		mediaFail(w, r, "count media", err)
		return
	}
	render(w, r, view.Media(h.page(r, "Media", "media"), view.MediaList{
		Items:      items,
		Page:       page,
		Pages:      int((total + mediaPerPage - 1) / mediaPerPage),
		CanDelete:  mediaCanDelete(r),
		Configured: h.media != nil,
	}))
}

func (h *Handler) mediaUpload(w http.ResponseWriter, r *http.Request) {
	if !h.mediaReady(w) {
		return
	}
	u, _ := auth.UserFrom(r.Context())

	// Refuse an oversize body before anything buffers it. media.Upload checks
	// the file itself; this covers the whole multipart envelope.
	r.Body = http.MaxBytesReader(w, r.Body, media.MaxSize+1<<20)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		http.Error(w, "That upload is too large or malformed.", http.StatusBadRequest)
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Choose a file to upload.", http.StatusBadRequest)
		return
	}
	defer f.Close()

	obj, err := h.media.Upload(r.Context(), hdr.Filename, f, hdr.Size, hdr.Header.Get("Content-Type"))
	switch {
	case errors.Is(err, media.ErrUnsupportedType):
		http.Error(w, "That file type is not supported. Upload a PNG, JPEG, GIF or WebP.", http.StatusBadRequest)
		return
	case errors.Is(err, media.ErrTooLarge):
		http.Error(w, "That file is larger than 10 MB.", http.StatusBadRequest)
		return
	case err != nil:
		mediaFail(w, r, "media upload", err)
		return
	}

	m, err := h.q.CreateMedia(r.Context(), db.CreateMediaParams{
		Key:        obj.Key,
		Filename:   obj.Filename,
		Url:        obj.URL,
		Mime:       obj.MIME,
		Size:       obj.Size,
		UploadedBy: u.ID,
		Alt:        "", // captioned afterwards, from the tile
	})
	if err != nil {
		// Without a row nothing would ever reference, list or clean up the
		// object, so drop it rather than leak storage.
		if delErr := h.media.Delete(r.Context(), obj.Key); delErr != nil {
			slog.ErrorContext(r.Context(), "orphaned upload", "key", obj.Key, "err", delErr)
		}
		mediaFail(w, r, "create media", err)
		return
	}
	render(w, r, view.MediaTile(m, mediaCanDelete(r)))
}

// mediaAlt sets the description of one image and swaps its tile back. It never
// touches object storage, so it works even with no bucket configured.
func (h *Handler) mediaAlt(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	alt := strings.TrimSpace(r.FormValue("alt"))
	// Rejected, not truncated: silently cutting someone's sentence in half is
	// worse than telling them it is too long.
	if utf8.RuneCountInString(alt) > mediaAltMax {
		http.Error(w, "That description is too long. Keep it under 300 characters.", http.StatusBadRequest)
		return
	}
	if err := h.q.UpdateMediaAlt(r.Context(), db.UpdateMediaAltParams{Alt: alt, ID: id}); err != nil {
		mediaFail(w, r, "update media alt", err)
		return
	}
	m, err := h.q.GetMedia(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		mediaFail(w, r, "get media", err)
		return
	}
	render(w, r, view.MediaTile(m, mediaCanDelete(r)))
}

func (h *Handler) mediaDelete(w http.ResponseWriter, r *http.Request) {
	if !h.mediaReady(w) {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	m, err := h.q.GetMedia(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		mediaFail(w, r, "get media", err)
		return
	}
	// An object that is already gone must not strand the row: the user would be
	// left with a broken tile they cannot clear.
	if err := h.media.Delete(r.Context(), m.Key); err != nil {
		slog.ErrorContext(r.Context(), "delete media object", "key", m.Key, "err", err)
	}
	if err := h.q.DeleteMedia(r.Context(), id); err != nil {
		mediaFail(w, r, "delete media", err)
		return
	}
	// Empty 200, not 204: htmx skips the swap on 204, and the tile has to go.
	w.WriteHeader(http.StatusOK)
}

// mediaReady guards the handlers that touch object storage. h.media is nil when
// no bucket is configured, and Kaku is meant to run without one.
func (h *Handler) mediaReady(w http.ResponseWriter) bool {
	if h.media == nil {
		http.Error(w, "Media storage is not configured. Set KAKU_S3_BUCKET to enable uploads.", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func mediaCanDelete(r *http.Request) bool {
	u, ok := auth.UserFrom(r.Context())
	return ok && slices.Contains(mediaDeleteRoles, u.Role)
}

// mediaFail logs the real cause and shows the user a generic one.
func mediaFail(w http.ResponseWriter, r *http.Request, msg string, err error) {
	slog.ErrorContext(r.Context(), msg, "err", err)
	http.Error(w, "Something went wrong.", http.StatusInternalServerError)
}
