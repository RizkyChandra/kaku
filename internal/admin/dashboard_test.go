package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/RizkyChandra/kaku/internal/auth"
	"github.com/RizkyChandra/kaku/internal/config"
	"github.com/RizkyChandra/kaku/internal/db"
)

// admHarness is the v0.2.0 chrome harness: a real database, a signed-in owner,
// and the router with no media store and no backup destination.
func admHarness(t *testing.T) (http.Handler, *db.Queries, *http.Cookie) {
	t.Helper()
	ctx := context.Background()

	sqlDB, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	q := db.New(sqlDB)
	hash, err := auth.HashPassword("password123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := q.CreateUser(ctx, db.CreateUserParams{
		Email: "owner@example.com", PasswordHash: hash, Name: "Owner", Role: auth.RoleOwner,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	a := auth.New(q, false)
	w := httptest.NewRecorder()
	if _, err := a.Login(ctx, w, "owner@example.com", "password123"); err != nil {
		t.Fatalf("login: %v", err)
	}
	return New(q, a, nil, nil, config.Config{Env: "development", DBPath: ":memory:"}).Router(),
		q, w.Result().Cookies()[0]
}

func admGet(t *testing.T, h http.Handler, path string, c *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// The API keys screen must be reachable from the nav, not only by typing the
// URL. This is the regression for issue #16.
func TestAdmNavLinksAPIKeys(t *testing.T) {
	h, _, c := admHarness(t)
	body := admGet(t, h, "/", c).Body.String()
	if !strings.Contains(body, `href="/admin/keys"`) {
		t.Error("sidebar nav has no link to /admin/keys")
	}
	if got := admGet(t, h, "/keys", c).Code; got != http.StatusOK {
		t.Errorf("/admin/keys = %d, want 200", got)
	}
}

func TestAdmDashboardCounts(t *testing.T) {
	h, q, c := admHarness(t)
	ctx := context.Background()
	u, _ := q.GetUserByEmail(ctx, "owner@example.com")
	for _, tc := range []struct{ slug, status string }{
		{"one", "published"}, {"two", "draft"}, {"three", "draft"},
	} {
		if _, err := q.CreatePost(ctx, db.CreatePostParams{
			Uuid: tc.slug, Type: "post", Title: "T " + tc.slug, Slug: tc.slug,
			Status: tc.status, Visibility: "public", AuthorID: u.ID, PublishedAt: nil,
		}); err != nil {
			t.Fatalf("create post: %v", err)
		}
	}

	body := admGet(t, h, "/", c).Body.String()
	for _, want := range []string{"Posts", "Drafts", "Recent posts", "T three"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	if !strings.Contains(body, "2 drafts waiting") {
		t.Error("dashboard did not report the two drafts")
	}
}

// site_title and footer_text are settings, so they must reach the chrome.
// Regression for issue #17.
func TestAdmChromeUsesSettings(t *testing.T) {
	h, q, c := admHarness(t)
	ctx := context.Background()
	for k, v := range map[string]string{"site_title": "Sumi Journal", "footer_text": "(c) Sumi"} {
		if err := q.SetSetting(ctx, db.SetSettingParams{Key: k, Value: v}); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}
	body := admGet(t, h, "/", c).Body.String()
	if !strings.Contains(body, "Sumi Journal") {
		t.Error("sidebar did not use site_title")
	}
	if !strings.Contains(body, "(c) Sumi") {
		t.Error("footer_text was not rendered")
	}
}

// A new post should start at the configured default visibility, and the editor
// must offer the control at all. Regression for issue #19.
func TestAdmEditorHasVisibilityControl(t *testing.T) {
	h, q, c := admHarness(t)
	if err := q.SetSetting(context.Background(), db.SetSettingParams{
		Key: "default_visibility", Value: "private",
	}); err != nil {
		t.Fatalf("set default_visibility: %v", err)
	}
	body := admGet(t, h, "/posts/new", c).Body.String()
	if !strings.Contains(body, `name="visibility"`) {
		t.Fatal("editor has no visibility control")
	}
	// The private option must be the selected one, not merely present.
	i := strings.Index(body, `value="private"`)
	if i < 0 || !strings.Contains(body[i:min(i+40, len(body))], "selected") {
		t.Error("default_visibility=private was not preselected")
	}
}

// Repeated failures must stop even a subsequently correct password, which is
// the observable proof the limiter is wired in. Regression for issue #18.
func TestAdmLoginThrottled(t *testing.T) {
	h, _, _ := admHarness(t)
	post := func(password string) int {
		form := url.Values{"email": {"owner@example.com"}, "password": {password}}
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "203.0.113.9:1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	for i := range 5 {
		if got := post("wrong"); got != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i+1, got)
		}
	}
	if got := post("password123"); got != http.StatusUnauthorized {
		t.Errorf("correct password after the limit = %d, want it throttled to 401", got)
	}
}

// With no bucket configured the panel explains itself instead of exploding.
// Part of issue #25.
func TestAdmBackupWithoutBucket(t *testing.T) {
	h, _, c := admHarness(t)
	req := httptest.NewRequest(http.MethodPost, "/backup", nil)
	req.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /admin/backup = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "KAKU_S3_BUCKET") {
		t.Error("panel did not name the variable to set")
	}
}
