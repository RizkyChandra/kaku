package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RizkyChandra/kaku/internal/i18n"
)

// The dashboard should come back in Japanese when the browser asks for it, with
// no cookie preference and no query parameter.
func TestLangFromAcceptLanguage(t *testing.T) {
	h, _, c := admHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(c)
	req.Header.Set("Accept-Language", "ja-JP,ja;q=0.9,en;q=0.8")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "記事") {
		t.Error("nav was not translated for Accept-Language: ja")
	}
	if !strings.Contains(body, `lang="ja"`) {
		t.Error(`<html lang> was not set to ja`)
	}
}

// ?lang= beats the browser, so a link can be shared in a specific language.
func TestLangFromQuery(t *testing.T) {
	h, _, c := admHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/?lang=id", nil)
	req.AddCookie(c)
	req.Header.Set("Accept-Language", "ja")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "Tulisan") {
		t.Error("?lang=id did not win over Accept-Language")
	}
}

// The stored preference beats everything, and survives the next request.
func TestLangPreferencePersists(t *testing.T) {
	h, q, c := admHarness(t)

	form := url.Values{"lang": {"ja"}, "next": {"/admin"}}
	req := httptest.NewRequest(http.MethodPost, "/language", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST /admin/language = %d, want 303", w.Code)
	}

	u, err := q.GetUserByEmail(t.Context(), "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if u.Locale != "ja" {
		t.Errorf("stored locale = %q, want ja", u.Locale)
	}

	// Even with the browser asking for Indonesian, the stored choice wins.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(c)
	req.Header.Set("Accept-Language", "id")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "記事") {
		t.Error("stored preference did not win over Accept-Language")
	}
}

// An unknown code must not error; it clears the preference back to the default.
func TestLangUnknownCodeIsIgnored(t *testing.T) {
	h, q, c := admHarness(t)
	form := url.Values{"lang": {"klingon"}, "next": {"/admin"}}
	req := httptest.NewRequest(http.MethodPost, "/language", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("= %d, want 303", w.Code)
	}
	u, _ := q.GetUserByEmail(t.Context(), "owner@example.com")
	if u.Locale != "" {
		t.Errorf("locale = %q, want it cleared", u.Locale)
	}
}

// The login page is outside the authenticated group but must still translate,
// or a user who cannot read English cannot get in.
func TestLoginPageTranslated(t *testing.T) {
	h, _, _ := admHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.Header.Set("Accept-Language", "id")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "Kata sandi") {
		t.Error("login page was not translated")
	}
}

// A language dropped into KAKU_LOCALES_DIR must reach the picker with no
// rebuild. This is the requirement the whole design exists for.
func TestDroppedInLocaleReachesThePicker(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "xx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nav.json"),
		[]byte(`{"nav.posts":"Poooosts"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "locale.json"),
		[]byte(`{"name":"Testish"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir = filepath.Dir(dir)
	if err := i18n.Load(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = i18n.Load("") })

	h, _, c := admHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/?lang=xx", nil)
	req.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	page := w.Body.String()
	if !strings.Contains(page, "Testish") {
		t.Error("dropped-in language missing from the picker")
	}
	if !strings.Contains(page, "Poooosts") {
		t.Error("dropped-in translation was not used")
	}
	// Keys it does not define must fall back to English, not render blank.
	if !strings.Contains(page, "Settings") {
		t.Error("untranslated key did not fall back to English")
	}
}
