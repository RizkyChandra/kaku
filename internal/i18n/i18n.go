// Package i18n translates the admin interface.
//
// A language is a directory of JSON files, not code:
//
//	locales/en/locale.json    {"name": "English", "dateFormat": "2 Jan 2006"}
//	locales/en/nav.json       {"nav.posts": "Posts", ...}
//	locales/en/posts.json     {"posts.title": "Posts", ...}
//
// The directory name is the language code. Every file except locale.json is a
// flat map of message key to text, split by screen purely so translators and
// merges have small files to work with - the loader concatenates them.
//
// These ship inside the binary, and anything in KAKU_LOCALES_DIR is loaded on
// top of them at startup, per file. Adding a language therefore needs no
// rebuild and no change here, which is the whole point of the design.
package i18n

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Fallback is the language every other one falls back to, key by key. Its file
// is the only one required to be complete.
const Fallback = "en"

//go:embed all:locales
var embedded embed.FS

// Locale is one language file.
type Locale struct {
	Code string `json:"code"`
	// Name is written in the language itself, so the picker reads correctly to
	// someone who cannot yet read the current one.
	Name       string            `json:"name"`
	DateFormat string            `json:"dateFormat"`
	Messages   map[string]string `json:"messages"`
}

var (
	mu      sync.RWMutex
	once    sync.Once
	locales = map[string]*Locale{}
	codes   []string // sorted, with Fallback first
)

// ensure loads the embedded locales on first read. main calls Load explicitly
// to pick up KAKU_LOCALES_DIR; this only stops a caller who forgot - a test,
// say - from rendering raw message keys instead of English.
func ensure() {
	once.Do(func() {
		mu.RLock()
		empty := len(locales) == 0
		mu.RUnlock()
		if empty {
			if err := load(""); err != nil {
				slog.Error("loading embedded locales", "err", err)
			}
		}
	})
}

// Load reads the embedded locales, then overlays any *.json in dir. A file in
// dir with the same code replaces the embedded one entirely, so an operator can
// correct a translation we shipped without waiting for a release.
//
// A broken file in dir is logged and skipped rather than fatal: a typo in an
// optional translation must not stop the CMS from booting.
func Load(dir string) error {
	once.Do(func() {}) // an explicit Load counts as the initialisation
	return load(dir)
}

func load(dir string) error {
	loaded := map[string]*Locale{}

	entries, err := fs.ReadDir(embedded, "locales")
	if err != nil {
		return fmt.Errorf("read embedded locales: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub, err := fs.Sub(embedded, "locales/"+e.Name())
		if err != nil {
			return err
		}
		if err := merge(loaded, e.Name(), sub); err != nil {
			return fmt.Errorf("embedded locale %s: %w", e.Name(), err) // ours, so fatal
		}
	}
	if _, ok := loaded[Fallback]; !ok {
		return fmt.Errorf("i18n: no %s locale, nothing to fall back to", Fallback)
	}

	// Overlay per file, not per language, so an operator can override one
	// screen without having to copy a whole translation forward on every
	// upgrade.
	if dir != "" {
		subdirs, _ := os.ReadDir(dir)
		for _, e := range subdirs {
			if !e.IsDir() {
				continue
			}
			if err := merge(loaded, e.Name(), os.DirFS(filepath.Join(dir, e.Name()))); err != nil {
				slog.Error("skipping locale directory", "dir", e.Name(), "err", err)
				continue
			}
			slog.Info("loaded locale from disk", "code", e.Name(), "dir", filepath.Join(dir, e.Name()))
		}
	}

	list := make([]string, 0, len(loaded))
	for code := range loaded {
		list = append(list, code)
	}
	slices.Sort(list)
	// Fallback leads the picker; it is the one guaranteed complete.
	if i := slices.Index(list, Fallback); i > 0 {
		list = slices.Insert(slices.Delete(list, i, i+1), 0, Fallback)
	}

	mu.Lock()
	defer mu.Unlock()
	locales, codes = loaded, list
	return nil
}

// merge folds one language directory into loaded, creating the locale if this
// is the first sight of that code. A single unreadable file aborts the
// directory rather than leaving a half-applied translation.
func merge(loaded map[string]*Locale, code string, fsys fs.FS) error {
	code = normalise(code)
	if code == "" {
		return fmt.Errorf("empty language code")
	}
	names, err := fs.Glob(fsys, "*.json")
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return fmt.Errorf("no .json files")
	}

	l := loaded[code]
	if l == nil {
		l = &Locale{Code: code, Name: code, DateFormat: "2 Jan 2006", Messages: map[string]string{}}
	}
	for _, name := range names {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if name == "locale.json" {
			var meta struct{ Name, DateFormat string }
			if err := json.Unmarshal(b, &meta); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			if meta.Name != "" {
				l.Name = meta.Name
			}
			if meta.DateFormat != "" {
				l.DateFormat = meta.DateFormat
			}
			continue
		}
		var msgs map[string]string
		if err := json.Unmarshal(b, &msgs); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		maps.Copy(l.Messages, msgs)
	}
	loaded[code] = l
	return nil
}

// Available lists the loaded locales for the language picker.
func Available() []Locale {
	ensure()
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Locale, 0, len(codes))
	for _, c := range codes {
		l := locales[c]
		out = append(out, Locale{Code: l.Code, Name: l.Name, DateFormat: l.DateFormat})
	}
	return out
}

// Get returns the locale for code, or nil.
func Get(code string) *Locale {
	ensure()
	mu.RLock()
	defer mu.RUnlock()
	return locales[normalise(code)]
}

// Negotiate returns the first preference that names a loaded locale, falling
// back to English. Preferences are tried in the order given, and each is also
// tried without its region, so "id-ID" finds "id".
func Negotiate(prefs ...string) *Locale {
	ensure()
	for _, p := range prefs {
		for _, want := range expand(p) {
			if l := Get(want); l != nil {
				return l
			}
		}
	}
	if l := Get(Fallback); l != nil {
		return l
	}
	// Only reachable if Load was never called; better an empty locale that
	// echoes keys than a nil dereference in a template.
	return &Locale{Code: Fallback, Name: Fallback, DateFormat: "2 Jan 2006"}
}

// expand turns one preference into the codes worth trying: an Accept-Language
// header is a list, and a tagged code should also match its base language.
func expand(pref string) []string {
	var out []string
	for _, part := range strings.Split(pref, ",") {
		// Drop any ";q=" weight. We take header order as the preference order
		// rather than sorting by q, which is close enough and much less code.
		tag, _, _ := strings.Cut(part, ";")
		tag = normalise(tag)
		if tag == "" || tag == "*" {
			continue
		}
		out = append(out, tag)
		if base, _, found := strings.Cut(tag, "-"); found {
			out = append(out, base)
		}
	}
	return out
}

func normalise(code string) string {
	return strings.ToLower(strings.TrimSpace(strings.ReplaceAll(code, "_", "-")))
}

// T looks up key, formatting it with args when any are given. A key missing
// from this locale falls back to English, and a key missing from English
// returns the key itself: an untranslated screen must still be usable, and a
// visible key is a better bug report than a blank button.
func (l *Locale) T(key string, args ...any) string {
	s, ok := l.Messages[key]
	if !ok {
		if en := Get(Fallback); en != nil && en != l {
			s, ok = en.Messages[key]
		}
	}
	if !ok {
		return key
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

// N is the plural form: it looks up key.one or key.other and formats it with n.
// Only those two forms exist. English, Indonesian and Japanese need nothing
// more, and a full CLDR plural table is a lot of machinery for a CMS admin.
// ponytail: if a language with dual/paucal forms is ever translated, this is
// where CLDR rules would go.
func (l *Locale) N(key string, n int64, args ...any) string {
	form := key + ".other"
	if n == 1 {
		form = key + ".one"
	}
	return l.T(form, append([]any{n}, args...)...)
}

type ctxKey struct{}

// WithLocale puts the locale on the context. Templates read it from there
// rather than taking it as a parameter, because several admin components are
// htmx fragments that are never handed the page struct.
func WithLocale(ctx context.Context, l *Locale) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// From returns the context's locale, never nil.
func From(ctx context.Context) *Locale {
	if l, ok := ctx.Value(ctxKey{}).(*Locale); ok && l != nil {
		return l
	}
	return Negotiate()
}

// T translates using the context's locale. This is what templates call.
func T(ctx context.Context, key string, args ...any) string {
	return From(ctx).T(key, args...)
}

// N is the plural form of T.
func N(ctx context.Context, key string, n int64, args ...any) string {
	return From(ctx).N(key, n, args...)
}
