package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/RizkyChandra/kaku/internal/auth"
	"github.com/RizkyChandra/kaku/internal/db"
)

// searchPost writes a post straight to the database: search must find what is
// stored, whatever route put it there. lang is optional and defaults to "en".
func searchPost(t *testing.T, q *db.Queries, author db.User, title, markdown string, lang ...string) db.Post {
	t.Helper()
	l := "en"
	if len(lang) > 0 {
		l = lang[0]
	}
	p, err := q.CreatePost(t.Context(), db.CreatePostParams{
		Uuid:       title, // unique enough for a test
		Type:       "post",
		Title:      title,
		Slug:       strings.ToLower(strings.ReplaceAll(title, " ", "-")),
		Markdown:   markdown,
		Status:     "draft",
		Visibility: "public",
		AuthorID:   author.ID,
		Lang:       l,
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}
	return p
}

// searchFor runs a query through the HTTP handler and returns the body.
func searchFor(t *testing.T, router http.Handler, c *http.Cookie, query string, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	return searchGet(t, router, c, "/admin/search?q="+url.QueryEscape(query), htmx)
}

// searchGet fetches any search URL, so page tests can add ?page=.
func searchGet(t *testing.T, router http.Handler, c *http.Cookie, path string, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.AddCookie(c)
	if htmx {
		r.Header.Set("HX-Request", "true")
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d: %s", path, w.Code, w.Body)
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

// Every row links to its post, so counting those links counts the page.
func searchRows(body string) int { return strings.Count(body, `href="/admin/posts/`) }

func TestSearchPaginates(t *testing.T) {
	h, q, router := newTest(t)
	u := newUser(t, q, "writer@example.com", auth.RoleAuthor)
	c := login(t, h, u)
	for i := 0; i < searchLimit+5; i++ {
		searchPost(t, q, u, "Wombat "+strconv.Itoa(i), "wombat sighting")
	}

	one := searchFor(t, router, c, "wombat", true).Body.String()
	if got := searchRows(one); got != searchLimit {
		t.Errorf("page 1 has %d rows, want %d", got, searchLimit)
	}
	two := searchGet(t, router, c, "/admin/search?q=wombat&page=2", true).Body.String()
	if got := searchRows(two); got != 5 {
		t.Errorf("page 2 has %d rows, want 5: %s", got, two)
	}

	// The pager has to carry the query, or page 2 searches for nothing.
	if !strings.Contains(one, "page=2") || !strings.Contains(one, "q=wombat") {
		t.Errorf("pager dropped the query: %s", one)
	}
	if !strings.Contains(two, "Page 2 of 2") {
		t.Errorf("page 2 pager is wrong: %s", two)
	}
}

func TestSearchHighlightsMatch(t *testing.T) {
	h, q, router := newTest(t)
	u := newUser(t, q, "writer@example.com", auth.RoleAuthor)
	c := login(t, h, u)
	searchPost(t, q, u, "Kyoto Notebook", "Ramen in a back alley near the river.")

	body := searchFor(t, router, c, "alley", true).Body.String()
	if !strings.Contains(body, "<mark") || !strings.Contains(body, "alley</mark>") {
		t.Errorf("match is not highlighted: %s", body)
	}
}

// The snippet is post body text, so it is escaped before the markers become
// markup. Get that order wrong and a post is a stored XSS.
func TestSearchEscapesSnippetHTML(t *testing.T) {
	h, q, router := newTest(t)
	u := newUser(t, q, "writer@example.com", auth.RoleAuthor)
	c := login(t, h, u)
	searchPost(t, q, u, "Innocent Title", "<script>alert(1)</script> aardvark")

	for _, htmx := range []bool{true, false} {
		body := searchFor(t, router, c, "aardvark", htmx).Body.String()
		if strings.Contains(body, "<script>alert(1)") {
			t.Errorf("htmx=%v: live script tag in the response: %s", htmx, body)
		}
		if !strings.Contains(body, "&lt;script&gt;alert(1)") {
			t.Errorf("htmx=%v: script tag was not escaped into the snippet: %s", htmx, body)
		}
		if !strings.Contains(body, "aardvark</mark>") {
			t.Errorf("htmx=%v: escaping lost the highlight: %s", htmx, body)
		}
	}
}

// Issue #31. unicode61 never split CJK, so the whole sentence was one token and
// no word inside it was findable. This is the reason the index is trigrams.
func TestSearchFindsJapaneseInsideASentence(t *testing.T) {
	h, q, router := newTest(t)
	u := newUser(t, q, "writer@example.com", auth.RoleAuthor)
	c := login(t, h, u)
	searchPost(t, q, u, "Nikki", "私は毎日書く")
	searchPost(t, q, u, "Betsu", "今日は寒い")

	for _, query := range []string{"書く", "毎日", "私は毎日"} {
		body := searchFor(t, router, c, query, false).Body.String()
		if !strings.Contains(body, "Nikki") {
			t.Errorf("query %q did not find the post: %s", query, body)
		}
		if strings.Contains(body, "Betsu") {
			t.Errorf("query %q matched an unrelated post", query)
		}
	}
}

// Under three characters there is no trigram to look up, so those queries take
// the substring scan instead of silently returning nothing.
func TestSearchShortQueryFallsBack(t *testing.T) {
	h, q, router := newTest(t)
	u := newUser(t, q, "writer@example.com", auth.RoleAuthor)
	c := login(t, h, u)
	searchPost(t, q, u, "Kyoto Notebook", "Ramen in a back alley.")
	searchPost(t, q, u, "Unrelated Piece", "Nothing to see.")

	// "yo" and "YO" are both inside "Kyoto": short queries stay case-blind too.
	for _, query := range []string{"yo", "YO", "am"} {
		body := searchFor(t, router, c, query, false).Body.String()
		if !strings.Contains(body, "Kyoto Notebook") {
			t.Errorf("short query %q found nothing: %s", query, body)
		}
	}
	if body := searchFor(t, router, c, "zz", false).Body.String(); strings.Contains(body, "Kyoto Notebook") {
		t.Errorf("short query matched a post it is not in: %s", body)
	}
}

// The filter narrows the rows and the count together, or page 2 is a lie.
func TestSearchFiltersByLanguage(t *testing.T) {
	h, q, router := newTest(t)
	u := newUser(t, q, "writer@example.com", auth.RoleAuthor)
	c := login(t, h, u)
	searchPost(t, q, u, "Wombat JA", "wombat sighting", "ja")
	for i := 0; i < searchLimit; i++ {
		searchPost(t, q, u, "Wombat "+strconv.Itoa(i), "wombat sighting")
	}

	all := searchGet(t, router, c, "/admin/search?q=wombat", true).Body.String()
	if got := searchRows(all); got != searchLimit {
		t.Fatalf("unfiltered page 1 has %d rows, want %d", got, searchLimit)
	}
	if !strings.Contains(all, "Page 1 of 2") {
		t.Errorf("unfiltered pager should span 2 pages: %s", all)
	}

	ja := searchGet(t, router, c, "/admin/search?q=wombat&lang=ja", true).Body.String()
	if got := searchRows(ja); got != 1 {
		t.Errorf("lang=ja has %d rows, want 1: %s", got, ja)
	}
	if !strings.Contains(ja, "Wombat JA") {
		t.Errorf("lang=ja lost the Japanese post: %s", ja)
	}
	// One row, so the pager must be gone entirely.
	if strings.Contains(ja, "Page 1 of") {
		t.Errorf("lang=ja kept the unfiltered total: %s", ja)
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

// Paging must keep the language filter. Without it page 2 silently widens back
// to every language, which reads as duplicate results appearing from nowhere.
func TestSearchPagerKeepsLanguage(t *testing.T) {
	h, q, router := newTest(t)
	u := newUser(t, q, "writer@example.com", auth.RoleAuthor)
	c := login(t, h, u)
	for i := 0; i <= searchLimit; i++ {
		searchPost(t, q, u, "Wombat JA "+strconv.Itoa(i), "wombat sighting", "ja")
	}
	body := searchGet(t, router, c, "/admin/search?q=wombat&lang=ja", true).Body.String()
	if !strings.Contains(body, "lang=ja") {
		t.Errorf("pager dropped the language filter: %s", body)
	}
}
