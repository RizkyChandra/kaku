package auth_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RizkyChandra/kaku/internal/auth"
	"github.com/RizkyChandra/kaku/internal/config"
	"github.com/RizkyChandra/kaku/internal/db"
)

func newDB(t *testing.T) (*sql.DB, *db.Queries) {
	t.Helper()
	sqlDB, err := db.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return sqlDB, db.New(sqlDB)
}

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := auth.HashPassword("correct horse")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !auth.CheckPassword(hash, "correct horse") {
		t.Error("correct password rejected")
	}
	if auth.CheckPassword(hash, "correct horsy") {
		t.Error("wrong password accepted")
	}
}

func TestEnsureRoot(t *testing.T) {
	ctx := context.Background()
	_, q := newDB(t)

	if err := auth.EnsureRoot(ctx, q, config.Config{}); err == nil {
		t.Error("empty database with no root env should error")
	}

	cfg := config.Config{RootEmail: "root@example.com", RootPassword: "hunter2222"}
	if err := auth.EnsureRoot(ctx, q, cfg); err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	u, err := q.GetUserByEmail(ctx, cfg.RootEmail)
	if err != nil {
		t.Fatalf("root user missing: %v", err)
	}
	if u.Role != auth.RoleOwner || !auth.CheckPassword(u.PasswordHash, cfg.RootPassword) {
		t.Fatalf("root user = role %q with unexpected password hash", u.Role)
	}

	if err := auth.EnsureRoot(ctx, q, cfg); err != nil {
		t.Fatalf("second EnsureRoot: %v", err)
	}
	if n, err := q.CountUsers(ctx); err != nil || n != 1 {
		t.Fatalf("CountUsers = %d, %v; want 1, nil", n, err)
	}

	cfg.RootPassword = "newpassword"
	if err := auth.EnsureRoot(ctx, q, cfg); err != nil {
		t.Fatalf("EnsureRoot with new password: %v", err)
	}
	u, err = q.GetUserByEmail(ctx, cfg.RootEmail)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.CheckPassword(u.PasswordHash, "newpassword") {
		t.Error("password was not updated from the environment")
	}

	// Users exist and no root env: no-op.
	if err := auth.EnsureRoot(ctx, q, config.Config{}); err != nil {
		t.Errorf("EnsureRoot with existing users: %v", err)
	}
}

func TestSessions(t *testing.T) {
	ctx := context.Background()
	sqlDB, q := newDB(t)
	cfg := config.Config{RootEmail: "root@example.com", RootPassword: "hunter2222"}
	if err := auth.EnsureRoot(ctx, q, cfg); err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	s := auth.New(q, false)

	guarded := s.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := auth.UserFrom(r.Context())
		if !ok {
			t.Error("no user in context")
		}
		w.Write([]byte(u.Email))
	}))

	if _, err := s.Login(ctx, httptest.NewRecorder(), cfg.RootEmail, "wrong"); err != auth.ErrInvalidCredentials {
		t.Errorf("Login with wrong password = %v; want ErrInvalidCredentials", err)
	}
	if _, err := s.Login(ctx, httptest.NewRecorder(), "nobody@example.com", "hunter2222"); err != auth.ErrInvalidCredentials {
		t.Errorf("Login with unknown email = %v; want ErrInvalidCredentials", err)
	}

	rec := httptest.NewRecorder()
	if _, err := s.Login(ctx, rec, cfg.RootEmail, cfg.RootPassword); err != nil {
		t.Fatalf("Login: %v", err)
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.CookieName {
			cookie = c
		}
	}
	if cookie == nil || cookie.Value == "" {
		t.Fatal("Login did not set a session cookie")
	}
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Error("session cookie is missing HttpOnly or SameSite=Lax")
	}

	var stored string
	if err := sqlDB.QueryRowContext(ctx, "SELECT id FROM sessions").Scan(&stored); err != nil {
		t.Fatalf("read session row: %v", err)
	}
	if stored == cookie.Value {
		t.Error("sessions.id stores the raw cookie value; it must be hashed")
	}

	r := httptest.NewRequest("GET", "/admin/posts", nil)
	r.AddCookie(cookie)
	rec = httptest.NewRecorder()
	guarded.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK || rec.Body.String() != cfg.RootEmail {
		t.Fatalf("authenticated request = %d %q", rec.Code, rec.Body.String())
	}

	r = httptest.NewRequest("GET", "/admin/posts", nil)
	r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "garbage"})
	rec = httptest.NewRecorder()
	guarded.ServeHTTP(rec, r)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("garbage cookie = %d; want 303 redirect", rec.Code)
	}

	r = httptest.NewRequest("POST", "/admin/posts", nil)
	rec = httptest.NewRecorder()
	guarded.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated POST = %d; want 401", rec.Code)
	}

	r = httptest.NewRequest("POST", "/admin/logout", nil)
	r.AddCookie(cookie)
	if err := s.Logout(ctx, httptest.NewRecorder(), r); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	r = httptest.NewRequest("GET", "/admin/posts", nil)
	r.AddCookie(cookie)
	rec = httptest.NewRecorder()
	guarded.ServeHTTP(rec, r)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("cookie still works after logout: %d", rec.Code)
	}
}
