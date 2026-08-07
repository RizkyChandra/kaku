package admin

import (
	"bytes"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"

	"github.com/RizkyChandra/kaku/internal/auth"
	"github.com/RizkyChandra/kaku/internal/config"
	"github.com/RizkyChandra/kaku/internal/db"
	"github.com/RizkyChandra/kaku/internal/media"
)

type mediaFixture struct {
	h      *Handler
	q      *db.Queries
	cookie *http.Cookie
}

// mediaSetup wires the admin UI to an in-memory database, a signed-in user of
// the given role, and (unless withStore is false) a real gofakes3 server.
func mediaSetup(t *testing.T, role string, withStore bool) mediaFixture {
	t.Helper()
	sqlDB, err := db.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)

	var store *media.Store
	if withStore {
		srv := httptest.NewServer(gofakes3.New(s3mem.New()).Server())
		t.Cleanup(srv.Close)
		store, err = media.New(t.Context(), config.S3{
			Endpoint:  srv.URL,
			Region:    "us-east-1",
			Bucket:    "kaku",
			AccessKey: "kaku",
			SecretKey: "kakukaku",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.EnsureBucket(t.Context()); err != nil {
			t.Fatal(err)
		}
	}

	hash, err := auth.HashPassword("kakukaku")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateUser(t.Context(), db.CreateUserParams{
		Email: "u@example.com", PasswordHash: hash, Name: "U", Role: role,
	}); err != nil {
		t.Fatal(err)
	}
	sessions := auth.New(q, false)
	rec := httptest.NewRecorder()
	if _, err := sessions.Login(t.Context(), rec, "u@example.com", "kakukaku"); err != nil {
		t.Fatal(err)
	}
	return mediaFixture{h: New(q, sessions, store, config.Config{}), q: q, cookie: rec.Result().Cookies()[0]}
}

func (f mediaFixture) do(req *http.Request) *httptest.ResponseRecorder {
	req.AddCookie(f.cookie)
	rec := httptest.NewRecorder()
	f.h.Router().ServeHTTP(rec, req)
	return rec
}

func (f mediaFixture) count(t *testing.T) int64 {
	t.Helper()
	n, err := f.q.CountMedia(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func mediaUploadRequest(t *testing.T, filename string, body []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/media", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func mediaPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestMediaUploadPNG(t *testing.T) {
	f := mediaSetup(t, auth.RoleAuthor, true)

	rec := f.do(mediaUploadRequest(t, "Cat Photo.PNG", mediaPNG(t)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if body := rec.Body.String(); !strings.Contains(body, "cat-photo.png") || !strings.Contains(body, `loading="lazy"`) {
		t.Errorf("fragment is not a tile for the upload: %s", body)
	}
	if n := f.count(t); n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
	// An author must not be offered a delete button they cannot use.
	if strings.Contains(rec.Body.String(), "/delete") {
		t.Error("author was shown a delete button")
	}
}

func TestMediaUploadRejectsNonImage(t *testing.T) {
	f := mediaSetup(t, auth.RoleAdmin, true)

	rec := f.do(mediaUploadRequest(t, "notes.txt", []byte("plain text, definitely not an image")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
	if n := f.count(t); n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
}

func TestMediaDelete(t *testing.T) {
	f := mediaSetup(t, auth.RoleAdmin, true)
	if rec := f.do(mediaUploadRequest(t, "gone.png", mediaPNG(t))); rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d: %s", rec.Code, rec.Body)
	}
	items, err := f.q.ListMedia(t.Context(), db.ListMediaParams{Limit: 1})
	if err != nil || len(items) != 1 {
		t.Fatalf("ListMedia = %v, %v", items, err)
	}

	rec := f.do(httptest.NewRequest(http.MethodPost, "/media/1/delete", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty so the swap removes the tile", rec.Body)
	}
	if n := f.count(t); n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
}

func TestMediaDeleteForbiddenForAuthors(t *testing.T) {
	f := mediaSetup(t, auth.RoleAuthor, true)
	if rec := f.do(mediaUploadRequest(t, "keep.png", mediaPNG(t))); rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d: %s", rec.Code, rec.Body)
	}
	if rec := f.do(httptest.NewRequest(http.MethodPost, "/media/1/delete", nil)); rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if n := f.count(t); n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
}

func TestMediaWithoutStore(t *testing.T) {
	f := mediaSetup(t, auth.RoleOwner, false)

	rec := f.do(httptest.NewRequest(http.MethodGet, "/media", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("index status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "no object storage is configured") {
		t.Error("index did not explain why uploads are off")
	}
	for _, req := range []*http.Request{
		mediaUploadRequest(t, "cat.png", mediaPNG(t)),
		httptest.NewRequest(http.MethodPost, "/media/1/delete", nil),
	} {
		if rec := f.do(req); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s status = %d, want 503", req.URL.Path, rec.Code)
		}
	}
}
