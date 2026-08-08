package db

import (
	"context"
	"log/slog"
	"strconv"
)

// Settings is the settings table flattened into a map, with defaults filled in
// for keys that were never saved.
type Settings map[string]string

// SettingDefaults are the values a fresh install behaves as if it had saved.
// They must agree with the Default fields in internal/admin/settings.go, which
// is what the form renders; this copy is what everything else reads.
var SettingDefaults = Settings{
	"site_title":         "Kaku",
	"language":           "en",
	"site_description":   "",
	"site_url":           "",
	"timezone":           "UTC",
	"default_visibility": "public",
	"posts_per_page":     "10",
	"footer_text":        "",
}

// LoadSettings never fails the caller: a settings read that errors should not
// take down a page or an API response, so it logs and returns the defaults.
func LoadSettings(ctx context.Context, q *Queries) Settings {
	s := make(Settings, len(SettingDefaults))
	for k, v := range SettingDefaults {
		s[k] = v
	}
	rows, err := q.ListSettings(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "load settings", "err", err)
		return s
	}
	for _, row := range rows {
		if row.Value != "" {
			s[row.Key] = row.Value
		}
	}
	return s
}

func (s Settings) Get(key string) string { return s[key] }

// Int returns the setting as an integer clamped to [lo, hi], falling back to
// def when it is missing or unparseable. Settings are validated on save, but an
// older row or a hand-edited database must not be able to produce a limit of
// zero or a million.
func (s Settings) Int(key string, def, lo, hi int64) int64 {
	n, err := strconv.ParseInt(s[key], 10, 64)
	if err != nil {
		return def
	}
	return min(max(n, lo), hi)
}
