package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RizkyChandra/kaku/internal/db"
)

const testKey = "TESTKEYTESTKEYTESTKEYTESTK"

// newAPI builds a router over a real in-memory database holding one published
// post, one draft, one private post, one page and one tagged post.
func newAPI(t *testing.T) http.Handler {
	t.Helper()
	h, _ := newAPIWithQueries(t)
	return h
}

func newAPIWithQueries(t *testing.T) (http.Handler, *db.Queries) {
	t.Helper()
	ctx := context.Background()
	sqlDB, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)

	u, err := q.CreateUser(ctx, db.CreateUserParams{
		Email: "writer@example.com", PasswordHash: "x", Name: "Aki Writer", Role: "owner",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := q.CreateApiKey(ctx, db.CreateApiKeyParams{
		Name: "test", KeyHash: HashKey(testKey), CreatedBy: u.ID,
	}); err != nil {
		t.Fatalf("create key: %v", err)
	}

	seed := func(slug, typ, status, visibility, published string) db.Post {
		t.Helper()
		p, err := q.CreatePost(ctx, db.CreatePostParams{
			Uuid: slug + "-uuid", Type: typ, Title: strings.ToUpper(slug), Slug: slug,
			Markdown: "SECRET_MARKDOWN", Html: "<p>hi</p>", Excerpt: "hi",
			Status: status, Visibility: visibility, AuthorID: u.ID, PublishedAt: published,
		})
		if err != nil {
			t.Fatalf("create post %s: %v", slug, err)
		}
		return p
	}
	seed("hello", "post", "published", "public", "2024-01-03 00:00:00")
	seed("second", "post", "published", "public", "2024-01-02 00:00:00")
	tagged := seed("third", "post", "published", "public", "2024-01-01 00:00:00")
	seed("a-draft", "post", "draft", "public", "2024-01-04 00:00:00")
	seed("a-secret", "post", "published", "private", "2024-01-05 00:00:00")
	seed("about", "page", "published", "public", "2024-01-01 00:00:00")

	tag, err := q.CreateTag(ctx, db.CreateTagParams{Name: "Go", Slug: "go", Description: "gophers"})
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}
	if err := q.AddPostTag(ctx, db.AddPostTagParams{PostID: tagged.ID, TagID: tag.ID}); err != nil {
		t.Fatalf("tag post: %v", err)
	}
	return New(q).Router(), q
}

func get(t *testing.T, h http.Handler, path, key string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if key != "" {
		r.Header.Set("Authorization", "Kaku "+key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return body
}

func TestAuth(t *testing.T) {
	h := newAPI(t)
	for _, tc := range []struct{ name, key string }{
		{"no key", ""},
		{"wrong key", "NOPENOPENOPENOPENOPENOPENO"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := get(t, h, "/posts", tc.key)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", w.Code)
			}
			if strings.Contains(w.Body.String(), "<") {
				t.Fatalf("body looks like HTML: %q", w.Body.String())
			}
			if decode(t, w)["error"] == nil {
				t.Fatalf("no error field: %q", w.Body.String())
			}
		})
	}
}

func TestListPostsHidesUnpublishedAndMarkdown(t *testing.T) {
	w := get(t, newAPI(t), "/posts", testKey)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "SECRET_MARKDOWN") || strings.Contains(w.Body.String(), `"markdown"`) {
		t.Fatalf("markdown leaked: %s", w.Body)
	}
	for _, hidden := range []string{"a-draft", "a-secret", "about"} {
		if strings.Contains(w.Body.String(), hidden) {
			t.Fatalf("%q must not appear in /posts: %s", hidden, w.Body)
		}
	}
	posts := decode(t, w)["posts"].([]any)
	if len(posts) != 3 {
		t.Fatalf("got %d posts, want 3", len(posts))
	}
	first := posts[0].(map[string]any)
	if first["slug"] != "hello" || first["author"] != "Aki Writer" {
		t.Fatalf("unexpected first post: %v", first)
	}
	if _, ok := first["id"]; ok {
		t.Fatalf("internal id leaked: %v", first)
	}
}

func TestPaginationMeta(t *testing.T) {
	h := newAPI(t)
	m := decode(t, get(t, h, "/posts?limit=2&page=2", testKey))
	posts := m["posts"].([]any)
	if len(posts) != 1 {
		t.Fatalf("got %d posts on page 2, want 1", len(posts))
	}
	meta := m["meta"].(map[string]any)
	for k, want := range map[string]float64{"page": 2, "limit": 2, "total": 3, "pages": 2} {
		if meta[k] != want {
			t.Errorf("meta[%q] = %v, want %v", k, meta[k], want)
		}
	}
}

func TestLimitIsCappedAndGarbageFallsBack(t *testing.T) {
	h := newAPI(t)
	for _, tc := range []struct {
		query string
		limit float64
	}{
		{"limit=99999", 100},
		{"limit=banana", 10},
		{"limit=-4&page=-1", 10},
	} {
		w := get(t, h, "/posts?"+tc.query, testKey)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", tc.query, w.Code)
		}
		meta := decode(t, w)["meta"].(map[string]any)
		if meta["limit"] != tc.limit {
			t.Errorf("%s: limit = %v, want %v", tc.query, meta["limit"], tc.limit)
		}
		if meta["page"].(float64) < 1 {
			t.Errorf("%s: page = %v", tc.query, meta["page"])
		}
	}
}

func TestSingleAndUnknownSlug(t *testing.T) {
	h := newAPI(t)

	post := decode(t, get(t, h, "/posts/hello", testKey))["post"].(map[string]any)
	if post["slug"] != "hello" {
		t.Fatalf("got %v", post)
	}

	page := decode(t, get(t, h, "/pages/about", testKey))["page"].(map[string]any)
	if page["slug"] != "about" {
		t.Fatalf("got %v", page)
	}

	for _, path := range []string{
		"/posts/nope",     // no such slug
		"/posts/about",    // a page, not a post
		"/pages/hello",    // a post, not a page
		"/posts/a-draft",  // unpublished
		"/posts/a-secret", // private
	} {
		w := get(t, h, path, testKey)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, w.Code)
		}
		if decode(t, w)["error"] == nil {
			t.Errorf("%s: no error field", path)
		}
	}
}

func TestTagsAndTagFilter(t *testing.T) {
	h := newAPI(t)

	tags := decode(t, get(t, h, "/tags", testKey))["tags"].([]any)
	if len(tags) != 1 || tags[0].(map[string]any)["slug"] != "go" {
		t.Fatalf("got %v", tags)
	}

	m := decode(t, get(t, h, "/posts?tag=go", testKey))
	posts := m["posts"].([]any)
	if len(posts) != 1 {
		t.Fatalf("got %d posts for tag go, want 1", len(posts))
	}
	first := posts[0].(map[string]any)
	if first["slug"] != "third" {
		t.Fatalf("got %v", first)
	}
	if got := first["tags"].([]any); len(got) != 1 {
		t.Fatalf("tags = %v", got)
	}
	if got := m["meta"].(map[string]any); got["total"] != float64(1) || got["pages"] != float64(1) {
		t.Errorf("tag-filtered meta = %v, want total 1 and pages 1", got)
	}

	if got := decode(t, get(t, h, "/posts?tag=missing", testKey))["posts"].([]any); len(got) != 0 {
		t.Errorf("unknown tag returned %v", got)
	}
}

func TestPreflightNeedsNoKey(t *testing.T) {
	w := httptest.NewRecorder()
	newAPI(t).ServeHTTP(w, httptest.NewRequest(http.MethodOptions, "/posts", nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("missing CORS header: %v", w.Header())
	}
}

// ?lang= narrows both the rows and meta.total; omitting it must keep returning
// every language, which is the pre-1.0 contract.
func TestAPILanguageFilter(t *testing.T) {
	h, q := newAPIWithQueries(t)
	ctx := t.Context()
	u, err := q.GetUserByEmail(ctx, "writer@example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ slug, lang, status string }{
		{"halo", "id", "published"},
		{"konnichiwa", "ja", "published"},
		{"draft-ja", "ja", "draft"},
	} {
		if _, err := q.CreatePost(ctx, db.CreatePostParams{
			Uuid: tc.slug, Type: "post", Title: tc.slug, Slug: tc.slug,
			Html: "<p>x</p>", Status: tc.status, Visibility: "public", AuthorID: u.ID,
			Lang: tc.lang, TranslationGroup: "grp", PublishedAt: "2024-02-01T00:00:00Z",
		}); err != nil {
			t.Fatalf("%s: %v", tc.slug, err)
		}
	}

	all := decode(t, get(t, h, "/posts", testKey))
	before := all["meta"].(map[string]any)["total"].(float64)

	ja := decode(t, get(t, h, "/posts?lang=ja", testKey))
	posts := ja["posts"].([]any)
	if len(posts) != 1 {
		t.Fatalf("lang=ja returned %d posts, want 1", len(posts))
	}
	first := posts[0].(map[string]any)
	if first["slug"] != "konnichiwa" || first["lang"] != "ja" {
		t.Errorf("got %v", first)
	}
	// The count must be filtered too, or the pager lies.
	if got := ja["meta"].(map[string]any)["total"].(float64); got != 1 {
		t.Errorf("lang=ja total = %v, want 1", got)
	}
	if before <= 1 {
		t.Errorf("unfiltered total = %v, expected every language", before)
	}

	// Siblings expose only published translations, never the draft.
	tr := first["translations"].(map[string]any)
	if tr["ja"] != "konnichiwa" || tr["id"] != "halo" {
		t.Errorf("translations = %v", tr)
	}
	if _, leaked := tr["draft-ja"]; leaked {
		t.Error("a draft translation leaked into the public API")
	}

	// An unknown language is an empty page, not an error.
	w := get(t, h, "/posts?lang=klingon", testKey)
	if w.Code != http.StatusOK {
		t.Fatalf("unknown lang = %d, want 200", w.Code)
	}
	if got := decode(t, w)["posts"].([]any); len(got) != 0 {
		t.Errorf("unknown lang returned %d posts", len(got))
	}
}
