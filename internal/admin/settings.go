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
	"github.com/RizkyChandra/kaku/internal/web/view"
)

// settingFields is the whole feature: rendering and saving both walk it, so a
// new setting is one line here. Infrastructure stays in the environment.
var settingFields = []view.SettingField{
	{
		Key: "site_title", Label: "Site title", Type: "text", Default: "Kaku",
		Help: "Shown as the name of the publication.", Validate: settingRequired,
	},
	{
		Key: "site_description", Label: "Site description", Type: "text",
		Help: "One line, used in feeds and meta tags.",
	},
	{
		Key: "site_url", Label: "Site URL", Type: "url",
		Help:     "Absolute links in the Content API are built from this. Leave blank to fall back to KAKU_URL.",
		Validate: settingURL,
	},
	{
		Key: "timezone", Label: "Timezone", Type: "text", Default: "UTC",
		Help: "IANA name, e.g. Asia/Tokyo. Publish times are read in this zone.", Validate: settingTimezone,
	},
	{
		Key: "default_visibility", Label: "Default post visibility", Type: "text", Default: "public",
		Options: []string{"public", "private"}, Help: "Applied to new posts.",
	},
	{
		Key: "posts_per_page", Label: "Posts per page", Type: "number", Default: "10",
		Help: "Page size for the Content API, between 1 and 100.", Validate: settingPerPage,
	},
	{
		Key: "footer_text", Label: "Footer line", Type: "text",
		Help: "Copyright or credit shown at the foot of the site.",
	},
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

	values := make(map[string]string, len(settingFields))
	errs := map[string]string{}
	for _, f := range settingFields {
		v := strings.TrimSpace(r.FormValue(f.Key))
		values[f.Key] = v
		switch {
		case len(f.Options) > 0 && !slices.Contains(f.Options, v):
			// A <select> proves nothing about what was posted.
			errs[f.Key] = "Choose one of the listed options."
		case f.Validate != nil:
			if msg := f.Validate(v); msg != "" {
				errs[f.Key] = msg
			}
		}
	}
	if len(errs) > 0 {
		renderStatus(w, r, http.StatusUnprocessableEntity, view.Settings(h.settingsData(r, values, errs, false)))
		return
	}

	for _, f := range settingFields {
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
	values := make(map[string]string, len(settingFields))
	for _, f := range settingFields {
		values[f.Key] = f.Default
		if v, ok := stored[f.Key]; ok {
			values[f.Key] = v
		}
	}
	return values, nil
}

func (h *Handler) settingsData(r *http.Request, values, errs map[string]string, saved bool) view.SettingsData {
	return view.SettingsData{
		Page:   h.page(r, "Settings", "settings"),
		Fields: settingFields,
		Values: values,
		Errors: errs,
		Env: []view.EnvRow{
			{Label: "Environment", Value: h.cfg.Env},
			{Label: "Version", Value: view.AssetVersion},
			{Label: "Listening on", Value: h.cfg.Addr},
			{Label: "KAKU_URL", Value: h.cfg.URL},
			{Label: "Database", Value: h.cfg.DBPath},
			{Label: "Media (S3 bucket)", Value: settingYesNo(h.cfg.S3.Bucket != "")},
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
			view.BackupPanel(true, "", "Backup failed. Check the server log."))
		return
	}
	render(w, r, view.BackupPanel(true, fmt.Sprintf("Backed up %s (%s).", res.Key, settingBytes(res.Size)), ""))
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

func settingYesNo(b bool) string {
	if b {
		return "configured"
	}
	return "not configured"
}

func settingRequired(v string) string {
	if v == "" {
		return "Required."
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
		return "Must be an absolute http:// or https:// URL."
	}
	return ""
}

func settingTimezone(v string) string {
	// LoadLocation("") quietly means UTC; make the operator say so.
	if _, err := time.LoadLocation(v); v == "" || err != nil {
		return "Unknown timezone. Use an IANA name like UTC or Asia/Tokyo."
	}
	return ""
}

func settingPerPage(v string) string {
	if n, err := strconv.Atoi(v); err != nil || n < 1 || n > 100 {
		return "Must be a whole number between 1 and 100."
	}
	return ""
}
