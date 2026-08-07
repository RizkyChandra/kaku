package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/RizkyChandra/kaku/internal/api"
	"github.com/RizkyChandra/kaku/internal/auth"
	"github.com/RizkyChandra/kaku/internal/config"
	"github.com/RizkyChandra/kaku/internal/db"
)

// apikeyPlaintext finds the key the create screen shows exactly once.
var apikeyPlaintext = regexp.MustCompile(`<code[^>]*>([A-Z2-7]{20,})</code>`)

// apikeyEnv signs in a user with the given role and returns the admin router,
// the queries it runs on, and the session cookie.
func apikeyEnv(t *testing.T, role string) (http.Handler, *db.Queries, *http.Cookie) {
	t.Helper()
	ctx := context.Background()
	sqlDB, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)

	hash, err := auth.HashPassword("hunter2hunter2")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := q.CreateUser(ctx, db.CreateUserParams{
		Email: "boss@example.com", PasswordHash: hash, Name: "Boss", Role: role,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	sessions := auth.New(q, false)
	w := httptest.NewRecorder()
	if _, err := sessions.Login(ctx, w, "boss@example.com", "hunter2hunter2"); err != nil {
		t.Fatalf("login: %v", err)
	}
	return New(q, sessions, nil, nil, config.Config{}).Router(), q, w.Result().Cookies()[0]
}

func apikeyDo(t *testing.T, h http.Handler, c *http.Cookie, method, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if form == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	r.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAPIKeyCreateShowsPlaintextOnceAndStoresOnlyTheHash(t *testing.T) {
	h, q, c := apikeyEnv(t, auth.RoleOwner)

	w := apikeyDo(t, h, c, http.MethodPost, "/keys", url.Values{"name": {"marketing site"}})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	m := apikeyPlaintext.FindStringSubmatch(w.Body.String())
	if m == nil {
		t.Fatalf("no plaintext key in the response: %s", w.Body)
	}
	plaintext := m[1]

	keys, err := q.ListApiKeys(context.Background())
	if err != nil {
		t.Fatalf("list keys: %v", err)
	}
	if len(keys) != 1 || keys[0].Name != "marketing site" {
		t.Fatalf("stored %v", keys)
	}
	if keys[0].KeyHash == plaintext {
		t.Fatal("the plaintext key was stored")
	}
	if keys[0].KeyHash != api.HashKey(plaintext) {
		t.Fatal("stored hash does not match what the API would look up")
	}

	// The list screen must not show it again.
	w = apikeyDo(t, h, c, http.MethodGet, "/keys", nil)
	if strings.Contains(w.Body.String(), plaintext) {
		t.Fatal("the key is shown after creation")
	}

	// And it can be revoked.
	if w = apikeyDo(t, h, c, http.MethodPost, "/keys/"+strconv.FormatInt(keys[0].ID, 10)+"/delete", url.Values{}); w.Code != http.StatusOK {
		t.Fatalf("delete status = %d: %s", w.Code, w.Body)
	}
	if keys, _ := q.ListApiKeys(context.Background()); len(keys) != 0 {
		t.Fatalf("key survived revocation: %v", keys)
	}
}

func TestAPIKeyEditorIsForbidden(t *testing.T) {
	h, _, c := apikeyEnv(t, auth.RoleEditor)
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/keys"},
		{http.MethodPost, "/keys"},
		{http.MethodPost, "/keys/1/delete"},
	} {
		var form url.Values
		if tc.method == http.MethodPost {
			form = url.Values{"name": {"sneaky"}}
		}
		if w := apikeyDo(t, h, c, tc.method, tc.path, form); w.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want 403", tc.method, tc.path, w.Code)
		}
	}
}
