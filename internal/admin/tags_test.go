package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/RizkyChandra/kaku/internal/auth"
	"github.com/RizkyChandra/kaku/internal/config"
	"github.com/RizkyChandra/kaku/internal/db"
)

// tagSetup wires the real admin router over a fresh in-memory database.
func tagSetup(t *testing.T) (http.Handler, *db.Queries, *auth.Service) {
	t.Helper()
	sqlDB, err := db.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	sessions := auth.New(q, false)
	return New(q, sessions, nil, config.Config{}).Router(), q, sessions
}

// tagSignIn creates a user with the given role and returns their session cookie.
func tagSignIn(t *testing.T, q *db.Queries, s *auth.Service, role string) *http.Cookie {
	t.Helper()
	hash, err := auth.HashPassword("correct horse")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	email := role + "@example.test"
	if _, err := q.CreateUser(t.Context(), db.CreateUserParams{
		Email: email, PasswordHash: hash, Name: role, Role: role,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	rec := httptest.NewRecorder()
	if _, err := s.Login(t.Context(), rec, email, "correct horse"); err != nil {
		t.Fatalf("login: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login set no cookie")
	}
	return cookies[0]
}

func tagPost(t *testing.T, h http.Handler, c *http.Cookie, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func tagNames(t *testing.T, q *db.Queries) []db.ListTagsRow {
	t.Helper()
	tags, err := q.ListTags(t.Context())
	if err != nil {
		t.Fatalf("list tags: %v", err)
	}
	return tags
}

func TestTagCreateDerivesSlug(t *testing.T) {
	h, q, s := tagSetup(t)
	c := tagSignIn(t, q, s, auth.RoleEditor)

	if got := tagPost(t, h, c, "/tags", url.Values{"name": {"Go Generics"}}).Code; got != http.StatusSeeOther {
		t.Fatalf("create: status %d", got)
	}
	tags := tagNames(t, q)
	if len(tags) != 1 || tags[0].Slug != "go-generics" {
		t.Fatalf("got %+v, want one tag slugged go-generics", tags)
	}
}

func TestTagCreateDuplicateNameSuffixesSlug(t *testing.T) {
	h, q, s := tagSetup(t)
	c := tagSignIn(t, q, s, auth.RoleAdmin)

	for range 2 {
		if got := tagPost(t, h, c, "/tags", url.Values{"name": {"Go"}}).Code; got != http.StatusSeeOther {
			t.Fatalf("create: status %d", got)
		}
	}
	tags := tagNames(t, q)
	if len(tags) != 2 {
		t.Fatalf("got %d tags, want 2", len(tags))
	}
	// ListTags orders by name, and both are "Go", so compare as a set of slugs.
	got := []string{tags[0].Slug, tags[1].Slug}
	if got[0] > got[1] {
		got[0], got[1] = got[1], got[0]
	}
	if got[0] != "go" || got[1] != "go-2" {
		t.Fatalf("got slugs %v, want [go go-2]", got)
	}
}

func TestTagCreateRejectsEmptyName(t *testing.T) {
	h, q, s := tagSetup(t)
	c := tagSignIn(t, q, s, auth.RoleEditor)

	rec := tagPost(t, h, c, "/tags", url.Values{"name": {"   "}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "A tag needs a name.") {
		t.Fatal("no inline error in the response")
	}
	if tags := tagNames(t, q); len(tags) != 0 {
		t.Fatalf("got %d tags, want none", len(tags))
	}
}

func TestTagCreateForbiddenForAuthor(t *testing.T) {
	h, q, s := tagSetup(t)
	c := tagSignIn(t, q, s, auth.RoleAuthor)

	if got := tagPost(t, h, c, "/tags", url.Values{"name": {"Go"}}).Code; got != http.StatusForbidden {
		t.Fatalf("status %d, want 403", got)
	}
	if tags := tagNames(t, q); len(tags) != 0 {
		t.Fatalf("got %d tags, want none", len(tags))
	}
}

func TestTagDeleteRemovesRow(t *testing.T) {
	h, q, s := tagSetup(t)
	c := tagSignIn(t, q, s, auth.RoleOwner)

	tag, err := q.CreateTag(t.Context(), db.CreateTagParams{Name: "Go", Slug: "go"})
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}
	path := "/tags/" + strconv.FormatInt(tag.ID, 10) + "/delete"
	if got := tagPost(t, h, c, path, url.Values{}).Code; got != http.StatusOK {
		t.Fatalf("delete: status %d", got)
	}
	if tags := tagNames(t, q); len(tags) != 0 {
		t.Fatalf("got %d tags, want none", len(tags))
	}
}
