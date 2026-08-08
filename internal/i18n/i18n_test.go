package i18n_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/RizkyChandra/kaku/internal/i18n"
)

func load(t *testing.T, dir string) {
	t.Helper()
	if err := i18n.Load(dir); err != nil {
		t.Fatalf("load(%q): %v", dir, err)
	}
	t.Cleanup(func() { _ = i18n.Load("") })
}

func TestEmbeddedLocales(t *testing.T) {
	load(t, "")
	got := i18n.Available()
	if len(got) < 2 {
		t.Fatalf("expected several locales, got %d", len(got))
	}
	// English leads: it is the one guaranteed complete.
	if got[0].Code != "en" {
		t.Errorf("first locale = %q, want en", got[0].Code)
	}
	if i18n.Get("ja").T("nav.posts") != "記事" {
		t.Errorf("ja nav.posts = %q", i18n.Get("ja").T("nav.posts"))
	}
}

// The point of the design: a language is a file, not a code change.
func TestDroppedInLocaleAppears(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "xx", "locale.json", `{"name":"Testish"}`)
	write(t, dir, "xx", "nav.json", `{"nav.posts":"Poooosts"}`)
	load(t, dir)

	l := i18n.Get("xx")
	if l == nil {
		t.Fatal("dropped-in locale was not loaded")
	}
	if l.Name != "Testish" {
		t.Errorf("name = %q", l.Name)
	}
	if got := l.T("nav.posts"); got != "Poooosts" {
		t.Errorf("nav.posts = %q", got)
	}
	// It must also show up in the picker, or nobody can select it.
	found := false
	for _, a := range i18n.Available() {
		found = found || a.Code == "xx"
	}
	if !found {
		t.Error("dropped-in locale missing from Available()")
	}
}

// A partial translation must degrade to English, never to a blank button.
func TestFallsBackKeyByKey(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "xx", "locale.json", `{"name":"Testish"}`)
	write(t, dir, "xx", "nav.json", `{"nav.posts":"Poooosts"}`)
	load(t, dir)

	l := i18n.Get("xx")
	if got := l.T("nav.settings"); got != "Settings" {
		t.Errorf("missing key = %q, want the English string", got)
	}
	if got := l.T("no.such.key.anywhere"); got != "no.such.key.anywhere" {
		t.Errorf("unknown key = %q, want the key echoed back", got)
	}
}

// A typo in an operator's own file must not stop the CMS booting.
func TestBrokenFileIsSkipped(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "bad", "nav.json", `{"nav.posts":`)
	write(t, dir, "ok", "locale.json", `{"name":"Fine"}`)
	load(t, dir)

	if i18n.Get("bad") != nil {
		t.Error("broken file was loaded")
	}
	if i18n.Get("ok") == nil {
		t.Error("valid file alongside a broken one was skipped")
	}
}

// An operator can correct a shipped translation without waiting for a release.
func TestDirOverridesEmbedded(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "id", "nav.json", `{"nav.posts":"Artikel"}`)
	load(t, dir)

	id := i18n.Get("id")
	if got := id.T("nav.posts"); got != "Artikel" {
		t.Errorf("nav.posts = %q, want the override", got)
	}
	// Overriding one file must not wipe the rest of that language.
	if got := id.T("nav.settings"); got != "Pengaturan" {
		t.Errorf("nav.settings = %q, want the shipped Indonesian", got)
	}
	if id.Name != "Bahasa Indonesia" {
		t.Errorf("name = %q, want the shipped one", id.Name)
	}
}

func TestNegotiate(t *testing.T) {
	load(t, "")
	for _, tc := range []struct {
		name  string
		prefs []string
		want  string
	}{
		{"first match wins", []string{"ja", "id"}, "ja"},
		{"skips unknown", []string{"zz", "id"}, "id"},
		{"region falls back to base", []string{"id-ID"}, "id"},
		{"underscores and case", []string{"JA_JP"}, "ja"},
		{"accept-language list", []string{"zz-ZZ,ja;q=0.9,en;q=0.8"}, "ja"},
		{"wildcard ignored", []string{"*"}, "en"},
		{"empty falls back", []string{"", ""}, "en"},
		{"nothing at all", nil, "en"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := i18n.Negotiate(tc.prefs...).Code; got != tc.want {
				t.Errorf("Negotiate(%q) = %q, want %q", tc.prefs, got, tc.want)
			}
		})
	}
}

func TestPlural(t *testing.T) {
	load(t, "")
	en := i18n.Get("en")
	if got := en.N("dashboard.draftsWaiting", 1); got != "1 draft waiting." {
		t.Errorf("one = %q", got)
	}
	if got := en.N("dashboard.draftsWaiting", 3); got != "3 drafts waiting." {
		t.Errorf("other = %q", got)
	}
}

func TestContext(t *testing.T) {
	load(t, "")
	ctx := i18n.WithLocale(context.Background(), i18n.Get("ja"))
	if got := i18n.T(ctx, "nav.tags"); got != "タグ" {
		t.Errorf("T = %q", got)
	}
	// A context that was never given a locale must still translate.
	if got := i18n.T(context.Background(), "nav.tags"); got != "Tags" {
		t.Errorf("bare context = %q, want English", got)
	}
}

// write drops one message file into dir/<code>/, the layout Load expects.
func write(t *testing.T, dir, code, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, code), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, code, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
