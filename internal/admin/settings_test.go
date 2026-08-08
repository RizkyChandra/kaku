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

// settingHarness returns a router backed by a real in-memory database and a
// session cookie for a user with the given role.
func settingHarness(t *testing.T, role string) (http.Handler, *db.Queries, *http.Cookie) {
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
		Email: "a@example.com", PasswordHash: hash, Name: "A", Role: role,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	a := auth.New(q, false)
	w := httptest.NewRecorder()
	if _, err := a.Login(ctx, w, "a@example.com", "password123"); err != nil {
		t.Fatalf("login: %v", err)
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login set no cookie")
	}
	return New(q, a, nil, nil, config.Config{Env: "development", DBPath: ":memory:"}).Router(), q, cookies[0]
}

// settingForm is a valid submission; overrides replace single fields.
func settingForm(overrides map[string]string) url.Values {
	v := url.Values{
		"language":           {"en"},
		"site_title":         {"Example"},
		"site_description":   {"A test site"},
		"site_url":           {"https://example.com"},
		"timezone":           {"Asia/Tokyo"},
		"default_visibility": {"public"},
		"posts_per_page":     {"25"},
		"footer_text":        {"© Example"},
	}
	for k, val := range overrides {
		v.Set(k, val)
	}
	return v
}

func settingPost(t *testing.T, r http.Handler, c *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(c)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSettingDefaultsRender(t *testing.T) {
	r, _, c := settingHarness(t, auth.RoleOwner)

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(c)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /settings = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{`value="Kaku"`, `value="UTC"`, `value="10"`, `id="site_url"`, "Environment"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// Field labels and help text live in Go, so they need their own proof that the
// screen is translated and not just the chrome around it.
func TestSettingRendersJapanese(t *testing.T) {
	r, _, c := settingHarness(t, auth.RoleOwner)

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(c)
	req.Header.Set("Accept-Language", "ja")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	body := w.Body.String()
	for _, want := range []string{"管理画面の言語", "タイムゾーン", "設定を保存", "待ち受けアドレス"} {
		if !strings.Contains(body, want) {
			t.Errorf("Japanese settings page missing %q", want)
		}
	}
	// Stored values must survive translation, or a save writes a translated word.
	if !strings.Contains(body, `value="UTC"`) || !strings.Contains(body, `<option value="public"`) {
		t.Error("a stored value was translated away")
	}
}

func TestSettingSaveRoundTrips(t *testing.T) {
	r, q, c := settingHarness(t, auth.RoleAdmin)

	w := settingPost(t, r, c, settingForm(nil))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST /settings = %d, want 303: %s", w.Code, w.Body.String())
	}

	for key, want := range map[string]string{
		"site_title":     "Example",
		"site_url":       "https://example.com",
		"timezone":       "Asia/Tokyo",
		"posts_per_page": "25",
	} {
		got, err := q.GetSetting(context.Background(), key)
		if err != nil || got != want {
			t.Errorf("GetSetting(%q) = %q, %v; want %q, nil", key, got, err, want)
		}
	}

	// Untouched defaults are not written.
	if _, err := q.GetSetting(context.Background(), "default_visibility"); err == nil {
		t.Error("unchanged default_visibility was written to the table")
	}
}

func TestSettingRejectsBadSiteURL(t *testing.T) {
	r, q, c := settingHarness(t, auth.RoleOwner)

	for _, bad := range []string{"javascript:alert(1)", "example.com", "data:text/html,x"} {
		w := settingPost(t, r, c, settingForm(map[string]string{"site_url": bad}))
		if w.Code != http.StatusUnprocessableEntity {
			t.Errorf("site_url %q = %d, want 422", bad, w.Code)
		}
		if !strings.Contains(w.Body.String(), "absolute http") {
			t.Errorf("site_url %q: no inline error rendered", bad)
		}
		if _, err := q.GetSetting(context.Background(), "site_url"); err == nil {
			t.Fatalf("site_url %q was saved", bad)
		}
	}
}

func TestSettingRejectsPostsPerPage(t *testing.T) {
	r, q, c := settingHarness(t, auth.RoleOwner)

	for _, bad := range []string{"0", "1000", "ten", ""} {
		w := settingPost(t, r, c, settingForm(map[string]string{"posts_per_page": bad}))
		if w.Code != http.StatusUnprocessableEntity {
			t.Errorf("posts_per_page %q = %d, want 422", bad, w.Code)
		}
		if _, err := q.GetSetting(context.Background(), "posts_per_page"); err == nil {
			t.Fatalf("posts_per_page %q was saved", bad)
		}
	}
}

// A rejected save re-renders what the user typed rather than dropping it.
func TestSettingRedisplaysInput(t *testing.T) {
	r, _, c := settingHarness(t, auth.RoleOwner)

	w := settingPost(t, r, c, settingForm(map[string]string{
		"site_title": "Kept Title", "posts_per_page": "0",
	}))
	if !strings.Contains(w.Body.String(), `value="Kept Title"`) {
		t.Error("form did not redisplay the submitted title")
	}
}

func TestSettingEditorForbidden(t *testing.T) {
	r, _, c := settingHarness(t, auth.RoleEditor)

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(c)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("editor GET /settings = %d, want 403", w.Code)
	}

	if got := settingPost(t, r, c, settingForm(nil)).Code; got != http.StatusForbidden {
		t.Errorf("editor POST /settings = %d, want 403", got)
	}
}
