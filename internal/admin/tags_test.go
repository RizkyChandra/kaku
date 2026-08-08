package admin

import (
	"fmt"
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
	return New(q, sessions, nil, nil, config.Config{}).Router(), q, sessions
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

func tagGet(t *testing.T, h http.Handler, c *http.Cookie, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// tagGetLang is tagGet for a reader browsing in another language.
func tagGetLang(t *testing.T, h http.Handler, c *http.Cookie, path, lang string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(c)
	req.Header.Set("Accept-Language", lang)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func tagNames(t *testing.T, q *db.Queries) []db.ListTagsRow {
	t.Helper()
	tags, err := q.ListTags(t.Context(), db.ListTagsParams{Limit: 1000})
	if err != nil {
		t.Fatalf("list tags: %v", err)
	}
	return tags
}

// tagMany creates n tags named tag-00, tag-01, ... in ListTags' own name order.
func tagMany(t *testing.T, q *db.Queries, n int) []db.Tag {
	t.Helper()
	tags := make([]db.Tag, n)
	for i := range n {
		name := fmt.Sprintf("tag-%02d", i)
		tag, err := q.CreateTag(t.Context(), db.CreateTagParams{Name: name, Slug: name})
		if err != nil {
			t.Fatalf("create tag: %v", err)
		}
		tags[i] = tag
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

func TestTagListPaginates(t *testing.T) {
	h, q, s := tagSetup(t)
	c := tagSignIn(t, q, s, auth.RoleEditor)
	tagMany(t, q, perPage+5)

	first := tagGet(t, h, c, "/tags").Body.String()
	if !strings.Contains(first, "tag-00") || strings.Contains(first, "tag-24") {
		t.Fatal("page 1 is not the first page of tags")
	}
	if !strings.Contains(first, "?page=2") {
		t.Fatal("page 1 has no link to page 2")
	}

	second := tagGet(t, h, c, "/tags?page=2").Body.String()
	if !strings.Contains(second, "tag-24") || strings.Contains(second, "tag-00") {
		t.Fatal("page 2 shows the same rows as page 1")
	}
}

func TestTagListClampsOutOfRangePage(t *testing.T) {
	h, q, s := tagSetup(t)
	c := tagSignIn(t, q, s, auth.RoleEditor)
	tagMany(t, q, perPage+5)

	for _, path := range []string{"/tags?page=99", "/tags?page=0", "/tags?page=-1", "/tags?page=nope"} {
		rec := tagGet(t, h, c, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "tag-") {
			t.Fatalf("GET %s rendered no rows", path)
		}
	}
}

// A tag past the first page used to 404 on every row route: tagLookup only ever
// scanned page one.
func TestTagEditWorksBeyondFirstPage(t *testing.T) {
	h, q, s := tagSetup(t)
	c := tagSignIn(t, q, s, auth.RoleEditor)
	last := tagMany(t, q, perPage+5)[perPage+4]
	id := strconv.FormatInt(last.ID, 10)

	for _, path := range []string{"/tags/" + id, "/tags/" + id + "/edit"} {
		rec := tagGet(t, h, c, path+"?posts=3")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), last.Name) {
			t.Fatalf("GET %s = %d, want 200 with the tag; body=%q", path, rec.Code, rec.Body.String())
		}
	}

	rec := tagPost(t, h, c, "/tags/"+id+"?posts=3", url.Values{"name": {"Renamed"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d, want 200", rec.Code)
	}
	// The post count is display-only and rides along the fragment routes.
	if !strings.Contains(rec.Body.String(), ">3<") {
		t.Fatalf("row lost its post count; body=%q", rec.Body.String())
	}
	if got, _ := q.GetTag(t.Context(), last.ID); got.Name != "Renamed" || got.Slug != "renamed" {
		t.Fatalf("tag = %+v, want renamed", got)
	}
}

func TestTagLookupUnknownIDIs404(t *testing.T) {
	h, q, s := tagSetup(t)
	c := tagSignIn(t, q, s, auth.RoleEditor)

	if got := tagGet(t, h, c, "/tags/999").Code; got != http.StatusNotFound {
		t.Fatalf("GET /tags/999 = %d, want 404", got)
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

// The screen and its htmx row fragments both translate: the fragment routes
// never see the page struct, so the locale has to reach them off the context.
func TestTagScreenTranslatedToJapanese(t *testing.T) {
	h, q, s := tagSetup(t)
	c := tagSignIn(t, q, s, auth.RoleEditor)

	for _, tc := range []struct{ path, want string }{
		{"/tags", "まだタグがありません。"},
		{"/tags", "タグを追加"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.AddCookie(c)
		req.Header.Set("Accept-Language", "ja")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("GET %s did not render %q", tc.path, tc.want)
		}
	}

	tag := tagMany(t, q, 1)[0]
	req := httptest.NewRequest(http.MethodGet, "/tags/"+strconv.FormatInt(tag.ID, 10)+"/edit", nil)
	req.AddCookie(c)
	req.Header.Set("Accept-Language", "ja")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "タグを保存") {
		t.Errorf("the edit fragment was not translated: %s", rec.Body)
	}
}

// A tag is one row shared by every language, with a name per language. Saving a
// Japanese name has to reach a Japanese reader's list, and leave everyone
// else's alone.
func TestTagTranslationLabelsTheList(t *testing.T) {
	h, q, s := tagSetup(t)
	c := tagSignIn(t, q, s, auth.RoleEditor)
	tag, err := q.CreateTag(t.Context(), db.CreateTagParams{Name: "Go", Slug: "go"})
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}
	// An untranslated neighbour: it has to keep reading as something.
	if _, err := q.CreateTag(t.Context(), db.CreateTagParams{Name: "Rust", Slug: "rust"}); err != nil {
		t.Fatalf("create tag: %v", err)
	}

	rec := tagPost(t, h, c, "/tags/"+strconv.FormatInt(tag.ID, 10)+"?posts=0", url.Values{
		"name": {"Go"}, "name_ja": {"ゴー言語"}, "description_ja": {"プログラミング言語"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d, want 200", rec.Code)
	}

	ja := tagGetLang(t, h, c, "/tags", "ja").Body.String()
	if !strings.Contains(ja, "ゴー言語") {
		t.Errorf("the Japanese list did not use the Japanese name: %s", ja)
	}
	if !strings.Contains(ja, "Rust") {
		t.Error("an untranslated tag lost its own name in the Japanese list")
	}
	if en := tagGetLang(t, h, c, "/tags", "en").Body.String(); strings.Contains(en, "ゴー言語") {
		t.Error("the Japanese name leaked into the English list")
	}
	// The slug stays shared: that is what makes ?tag=go find posts in every
	// language.
	if got, _ := q.GetTag(t.Context(), tag.ID); got.Slug != "go" {
		t.Errorf("slug = %q, want go", got.Slug)
	}
}

// An empty field means "not translated". Storing it as an empty name would win
// the COALESCE and render the tag blank, so it has to delete the row instead.
func TestTagTranslationClearedFallsBack(t *testing.T) {
	h, q, s := tagSetup(t)
	c := tagSignIn(t, q, s, auth.RoleEditor)
	tag, err := q.CreateTag(t.Context(), db.CreateTagParams{Name: "Go", Slug: "go"})
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}
	path := "/tags/" + strconv.FormatInt(tag.ID, 10) + "?posts=0"

	tagPost(t, h, c, path, url.Values{"name": {"Go"}, "name_ja": {"ゴー言語"}})
	tagPost(t, h, c, path, url.Values{"name": {"Go"}, "name_ja": {"  "}})

	if trans, err := q.ListTagTranslations(t.Context(), tag.ID); err != nil || len(trans) != 0 {
		t.Fatalf("translations = %+v (err %v), want none", trans, err)
	}
	body := tagGetLang(t, h, c, "/tags", "ja").Body.String()
	if strings.Contains(body, "ゴー言語") {
		t.Error("the cleared translation is still on the screen")
	}
	if !strings.Contains(body, "Go") {
		t.Errorf("the tag rendered blank instead of falling back to its own name: %s", body)
	}
}

// Tag names and their translations are user input rendered back into a page.
func TestTagNamesAreEscaped(t *testing.T) {
	h, q, s := tagSetup(t)
	c := tagSignIn(t, q, s, auth.RoleEditor)
	tag, err := q.CreateTag(t.Context(), db.CreateTagParams{Name: `<script>alert(1)</script>`, Slug: "x"})
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}
	id := strconv.FormatInt(tag.ID, 10)
	tagPost(t, h, c, "/tags/"+id+"?posts=0", url.Values{
		"name": {`<script>alert(1)</script>`}, "name_ja": {`<script>alert(2)</script>`},
	})

	for _, tc := range []struct{ path, lang string }{
		{"/tags", "en"}, {"/tags", "ja"}, {"/tags/" + id + "/edit?posts=0", "ja"},
	} {
		body := tagGetLang(t, h, c, tc.path, tc.lang).Body.String()
		if strings.Contains(body, "<script>alert") {
			t.Errorf("GET %s in %s rendered a raw script tag: %s", tc.path, tc.lang, body)
		}
		if !strings.Contains(body, "&lt;script&gt;") {
			t.Errorf("GET %s in %s did not render the escaped name at all", tc.path, tc.lang)
		}
	}
}

// The edit fragment offers one field per loaded language, prefilled.
func TestTagEditRowHasAFieldPerLanguage(t *testing.T) {
	h, q, s := tagSetup(t)
	c := tagSignIn(t, q, s, auth.RoleEditor)
	tag, err := q.CreateTag(t.Context(), db.CreateTagParams{Name: "Go", Slug: "go"})
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}
	if err := q.SetTagTranslation(t.Context(), db.SetTagTranslationParams{
		TagID: tag.ID, Lang: "ja", Name: "ゴー言語", Description: "説明",
	}); err != nil {
		t.Fatalf("set translation: %v", err)
	}

	body := tagGetLang(t, h, c, "/tags/"+strconv.FormatInt(tag.ID, 10)+"/edit?posts=0", "en").Body.String()
	for _, want := range []string{`name="name_en"`, `name="name_ja"`, `name="name_id"`, `name="description_ja"`, "ゴー言語"} {
		if !strings.Contains(body, want) {
			t.Errorf("the edit fragment is missing %q: %s", want, body)
		}
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
