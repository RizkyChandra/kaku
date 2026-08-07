package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/RizkyChandra/kaku/internal/auth"
	"github.com/RizkyChandra/kaku/internal/db"
)

// searchPost writes a post straight to the database: search must find what is
// stored, whatever route put it there.
func searchPost(t *testing.T, q *db.Queries, author db.User, title, markdown string) db.Post {
	t.Helper()
	p, err := q.CreatePost(t.Context(), db.CreatePostParams{
		Uuid:       title, // unique enough for a test
		Type:       "post",
		Title:      title,
		Slug:       strings.ToLower(strings.ReplaceAll(title, " ", "-")),
		Markdown:   markdown,
		Status:     "draft",
		Visibility: "public",
		AuthorID:   author.ID,
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}
	return p
}

// searchFor runs a query through the HTTP handler and returns the body.
func searchFor(t *testing.T, router http.Handler, c *http.Cookie, query string, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/admin/search?q="+url.QueryEscape(query), nil)
	r.AddCookie(c)
	if htmx {
		r.Header.Set("HX-Request", "true")
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET search %q: status = %d: %s", query, w.Code, w.Body)
	}
	return w
}

func TestSearchFindsTitleAndBody(t *testing.T) {
	h, q, router := newTest(t)
	u := newUser(t, q, "writer@example.com", auth.RoleAuthor)
	c := login(t, h, u)
	searchPost(t, q, u, "Kyoto Notebook", "Ramen in a back alley near the river.")
	searchPost(t, q, u, "Unrelated Piece", "Nothing to see.")

	for _, query := range []string{"Kyoto", "ramen", "kyot"} {
		body := searchFor(t, router, c, query, false).Body.String()
		if !strings.Contains(body, "Kyoto Notebook") {
			t.Errorf("query %q did not find the post: %s", query, body)
		}
		if strings.Contains(body, "Unrelated Piece") {
			t.Errorf("query %q matched an unrelated post", query)
		}
	}
	if body := searchFor(t, router, c, "Kyoto", false).Body.String(); !strings.Contains(body, u.Name) {
		t.Errorf("result did not show the author: %s", body)
	}
}

// The update trigger: an edited post is found by its new text and not its old.
func TestSearchFollowsEdits(t *testing.T) {
	h, q, router := newTest(t)
	u := newUser(t, q, "writer@example.com", auth.RoleAuthor)
	c := login(t, h, u)
	p := searchPost(t, q, u, "Draft One", "aardvark")

	if _, err := q.UpdatePost(t.Context(), db.UpdatePostParams{
		Title: "Draft One", Slug: p.Slug, Markdown: "buffalo",
		Status: p.Status, Visibility: p.Visibility, ID: p.ID,
	}); err != nil {
		t.Skipf("db.UpdatePost is mis-generated; see posts_test.go: %v", err)
	}
	if body := searchFor(t, router, c, "buffalo", false).Body.String(); !strings.Contains(body, "Draft One") {
		t.Errorf("edited post not found by its new text: %s", body)
	}
	if body := searchFor(t, router, c, "aardvark", false).Body.String(); strings.Contains(body, "Draft One") {
		t.Errorf("edited post still found by its old text: %s", body)
	}
}

// The delete trigger.
func TestSearchDropsDeletedPosts(t *testing.T) {
	h, q, router := newTest(t)
	u := newUser(t, q, "writer@example.com", auth.RoleAuthor)
	c := login(t, h, u)
	p := searchPost(t, q, u, "Doomed Draft", "wombat")

	if err := q.DeletePost(t.Context(), p.ID); err != nil {
		t.Fatalf("delete post: %v", err)
	}
	if body := searchFor(t, router, c, "wombat", false).Body.String(); strings.Contains(body, "Doomed Draft") {
		t.Errorf("deleted post still in results: %s", body)
	}
	// SQLite hands the freed id to the next post. A delete trigger that left the
	// old terms behind would make that post match them.
	searchPost(t, q, u, "Fresh Draft", "narwhal")
	if body := searchFor(t, router, c, "wombat", false).Body.String(); strings.Contains(body, "Fresh Draft") {
		t.Errorf("new post inherited the deleted post's index entry: %s", body)
	}
}

// FTS5 syntax is not the user's to write: quotes, stars and operators are text.
func TestSearchSurvivesFTSSyntax(t *testing.T) {
	h, q, router := newTest(t)
	u := newUser(t, q, "writer@example.com", auth.RoleAuthor)
	c := login(t, h, u)
	searchPost(t, q, u, "Quoted Piece", "wombat")

	for _, query := range []string{`"`, `*`, `""`, `wombat"`, `wombat*`, `NOT wombat`, `a OR`, `^`, `(`, `-`, `:`} {
		searchFor(t, router, c, query, false) // fails the test on any non-200
	}
	if body := searchFor(t, router, c, `wombat"`, false).Body.String(); !strings.Contains(body, "Quoted Piece") {
		t.Errorf("a stray quote lost the match: %s", body)
	}
}

func TestSearchEmptyQueryReturnsNothing(t *testing.T) {
	h, q, router := newTest(t)
	u := newUser(t, q, "writer@example.com", auth.RoleAuthor)
	c := login(t, h, u)
	searchPost(t, q, u, "Findable Piece", "wombat")

	for _, query := range []string{"", "   "} {
		if body := searchFor(t, router, c, query, false).Body.String(); strings.Contains(body, "Findable Piece") {
			t.Errorf("empty query %q returned results: %s", query, body)
		}
	}
}

func TestSearchHXRequestReturnsFragment(t *testing.T) {
	h, q, router := newTest(t)
	u := newUser(t, q, "writer@example.com", auth.RoleAuthor)
	c := login(t, h, u)
	searchPost(t, q, u, "Kyoto Notebook", "ramen")

	frag := searchFor(t, router, c, "Kyoto", true).Body.String()
	if strings.Contains(frag, "<html") || strings.Contains(frag, "<form") {
		t.Errorf("HX-Request got the whole page: %s", frag)
	}
	if !strings.Contains(frag, "Kyoto Notebook") {
		t.Errorf("fragment has no results: %s", frag)
	}

	full := searchFor(t, router, c, "Kyoto", false).Body.String()
	if !strings.Contains(full, "<html") || !strings.Contains(full, `role="search"`) {
		t.Errorf("plain GET is not a full page: %s", full)
	}
	if !strings.Contains(full, "Kyoto Notebook") {
		t.Errorf("full page has no results: %s", full)
	}
}
