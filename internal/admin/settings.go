package admin

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	// The release image is distroless, so the host may carry no zoneinfo at all
	// and every LoadLocation would fail. Embed the database instead.
	_ "time/tzdata"

	"github.com/go-chi/chi/v5"

	"github.com/RizkyChandra/kaku/internal/auth"
	"github.com/RizkyChandra/kaku/internal/db"
	"github.com/RizkyChandra/kaku/internal/i18n"
	"github.com/RizkyChandra/kaku/internal/web/view"
)

// settingFields is the whole feature: rendering and saving both walk it, so a
// new setting is one line here. Infrastructure stays in the environment.
// settingFieldList is a function, not a var, because the language options come
// from whatever locales are loaded at runtime - including any dropped into
// KAKU_LOCALES_DIR, which are not known at package init.
func settingFieldList() []view.SettingField {
	langs := i18n.Available()
	codes := make([]string, 0, len(langs))
	for _, l := range langs {
		codes = append(codes, l.Code)
	}
	// Label and Help are message keys; the setting keys themselves, the option
	// values and the defaults are stored data and stay as they are.
	return []view.SettingField{
		{
			Key: "language", Label: "settings.language.label", Type: "text", Default: i18n.Fallback,
			Options: codes,
			Help:    "settings.language.help",
		},
		{
			Key: "site_title", Label: "settings.siteTitle.label", Type: "text", Default: "Kaku",
			Help: "settings.siteTitle.help", Validate: settingRequired,
		},
		{
			Key: "site_description", Label: "settings.siteDescription.label", Type: "text",
			Help: "settings.siteDescription.help",
		},
		{
			Key: "site_url", Label: "settings.siteUrl.label", Type: "url",
			Help:     "settings.siteUrl.help",
			Validate: settingURL,
		},
		{
			Key: "timezone", Label: "settings.timezone.label", Type: "text", Default: "UTC",
			Help: "settings.timezone.help", Validate: settingTimezone,
		},
		{
			Key: "default_visibility", Label: "settings.defaultVisibility.label", Type: "text", Default: "public",
			Options: []string{"public", "private"}, Help: "settings.defaultVisibility.help",
		},
		{
			Key: "posts_per_page", Label: "settings.postsPerPage.label", Type: "number", Default: "10",
			Help: "settings.postsPerPage.help", Validate: settingPerPage,
		},
		{
			Key: "footer_text", Label: "settings.footerText.label", Type: "text",
			Help: "settings.footerText.help",
		},
	}
}

func (h *Handler) mountSettings(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireRole(auth.RoleOwner, auth.RoleAdmin))
		r.Get("/settings", h.settings)
		r.Post("/settings", h.saveSettings)
		r.Post("/backup", h.backupNow)
	})
}

func (h *Handler) settings(w http.ResponseWriter, r *http.Request) {
	values, err := h.settingValues(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "settings", "err", err)
		http.Error(w, "something went wrong", http.StatusInternalServerError)
		return
	}
	render(w, r, view.Settings(h.settingsData(r, values, nil, r.URL.Query().Has("saved"))))
}

func (h *Handler) saveSettings(w http.ResponseWriter, r *http.Request) {
	current, err := h.settingValues(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "settings", "err", err)
		http.Error(w, "something went wrong", http.StatusInternalServerError)
		return
	}

	values := make(map[string]string, len(settingFieldList()))
	errs := map[string]string{}
	for _, f := range settingFieldList() {
		v := strings.TrimSpace(r.FormValue(f.Key))
		values[f.Key] = v
		switch {
		case len(f.Options) > 0 && !slices.Contains(f.Options, v):
			// A <select> proves nothing about what was posted.
			errs[f.Key] = i18n.T(r.Context(), "settings.error.options")
		case f.Validate != nil:
			// Validators have no context, so they return a key and we resolve it.
			if key := f.Validate(v); key != "" {
				errs[f.Key] = i18n.T(r.Context(), key)
			}
		}
	}
	if len(errs) > 0 {
		renderStatus(w, r, http.StatusUnprocessableEntity, view.Settings(h.settingsData(r, values, errs, false)))
		return
	}

	for _, f := range settingFieldList() {
		if values[f.Key] == current[f.Key] {
			continue
		}
		if err := h.q.SetSetting(r.Context(), db.SetSettingParams{Key: f.Key, Value: values[f.Key]}); err != nil {
			slog.ErrorContext(r.Context(), "save setting", "key", f.Key, "err", err)
			http.Error(w, "something went wrong", http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, "/admin/settings?saved=1", http.StatusSeeOther)
}

// settingValues reads the table once and fills in a default for every key that
// has never been saved.
func (h *Handler) settingValues(ctx context.Context) (map[string]string, error) {
	rows, err := h.q.ListSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}
	stored := make(map[string]string, len(rows))
	for _, s := range rows {
		stored[s.Key] = s.Value
	}
	values := make(map[string]string, len(settingFieldList()))
	for _, f := range settingFieldList() {
		values[f.Key] = f.Default
		if v, ok := stored[f.Key]; ok {
			values[f.Key] = v
		}
	}
	return values, nil
}

func (h *Handler) settingsData(r *http.Request, values, errs map[string]string, saved bool) view.SettingsData {
	ctx := r.Context()
	return view.SettingsData{
		Page:   h.page(r, i18n.T(ctx, "settings.title"), "settings"),
		Fields: settingFieldList(),
		Values: values,
		Errors: errs,
		Env: []view.EnvRow{
			{Label: "settings.env.environment", Value: h.cfg.Env},
			{Label: "settings.env.version", Value: view.AssetVersion},
			{Label: "settings.env.listening", Value: h.cfg.Addr},
			{Label: "KAKU_URL", Value: h.cfg.URL}, // a variable name, not a word
			{Label: "settings.env.database", Value: h.cfg.DBPath},
			{Label: "settings.env.media", Value: settingYesNo(ctx, h.cfg.S3.Bucket != "")},
		},
		Saved:          saved,
		BackupsEnabled: h.backups != nil,
	}
}

// backupNow runs a backup on demand and swaps the panel with the outcome. It is
// slow by nature, so nothing here is done in a goroutine: the operator asked and
// should be told whether it worked.
func (h *Handler) backupNow(w http.ResponseWriter, r *http.Request) {
	if h.backups == nil {
		render(w, r, view.BackupPanel(false, "", ""))
		return
	}
	res, err := h.backups.Once(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "manual backup", "err", err)
		renderStatus(w, r, http.StatusInternalServerError,
			view.BackupPanel(true, "", i18n.T(r.Context(), "settings.backup.failed")))
		return
	}
	render(w, r, view.BackupPanel(true,
		i18n.T(r.Context(), "settings.backup.done", res.Key, settingBytes(res.Size)), ""))
}

// settingBytes is a human size for the backup message; three units is enough for
// a SQLite file.
func settingBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
}

func settingYesNo(ctx context.Context, b bool) string {
	if b {
		return i18n.T(ctx, "settings.env.configured")
	}
	return i18n.T(ctx, "settings.env.notConfigured")
}

// The validators below return a message key, not a message: they are values in
// settingFieldList and so never see a request.
func settingRequired(v string) string {
	if v == "" {
		return "settings.error.required"
	}
	return ""
}

// settingURL is a trust boundary: this value ends up in generated links, so
// anything that is not an absolute http(s) URL is rejected outright.
func settingURL(v string) string {
	if v == "" {
		return ""
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "settings.error.url"
	}
	return ""
}

func settingTimezone(v string) string {
	// LoadLocation("") quietly means UTC; make the operator say so.
	if _, err := time.LoadLocation(v); v == "" || err != nil {
		return "settings.error.timezone"
	}
	return ""
}

func settingPerPage(v string) string {
	if n, err := strconv.Atoi(v); err != nil || n < 1 || n > 100 {
		return "settings.error.perPage"
	}
	return ""
}
