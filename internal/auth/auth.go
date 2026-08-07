// Package auth hashes passwords, issues cookie sessions, and guards the admin
// routes.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/RizkyChandra/kaku/internal/config"
	"github.com/RizkyChandra/kaku/internal/db"
)

const (
	CookieName = "kaku_session"
	// Must match the '+14 days' modifier in internal/db/queries/sessions.sql.
	sessionLifetime = 14 * 24 * time.Hour

	RoleOwner       = "owner"
	RoleAdmin       = "admin"
	RoleEditor      = "editor"
	RoleAuthor      = "author"
	RoleContributor = "contributor"
)

// ErrInvalidCredentials covers both an unknown email and a wrong password, so
// login cannot be used to enumerate accounts.
var ErrInvalidCredentials = errors.New("invalid email or password")

// Compared against when the email is unknown, so a login attempt costs the same
// whether or not the account exists.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("kaku"), bcrypt.DefaultCost)

func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(h), nil
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

type Service struct {
	q             *db.Queries
	secureCookies bool
}

func New(q *db.Queries, secureCookies bool) *Service {
	return &Service{q: q, secureCookies: secureCookies}
}

// Login verifies the credentials, stores a session and sets its cookie.
func (s *Service) Login(ctx context.Context, w http.ResponseWriter, email, password string) (db.User, error) {
	u, err := s.q.GetUserByEmail(ctx, email)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return db.User{}, fmt.Errorf("lookup user: %w", err)
		}
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return db.User{}, ErrInvalidCredentials
	}
	if !CheckPassword(u.PasswordHash, password) {
		return db.User{}, ErrInvalidCredentials
	}

	token, err := newToken()
	if err != nil {
		return db.User{}, err
	}
	if err := s.q.CreateSession(ctx, db.CreateSessionParams{ID: hashToken(token), UserID: u.ID}); err != nil {
		return db.User{}, fmt.Errorf("create session: %w", err)
	}
	http.SetCookie(w, s.cookie(token, int(sessionLifetime.Seconds())))
	return u, nil
}

// Logout deletes the session row and clears the cookie.
func (s *Service) Logout(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(CookieName); err == nil {
		if err := s.q.DeleteSession(ctx, hashToken(c.Value)); err != nil {
			return fmt.Errorf("delete session: %w", err)
		}
	}
	http.SetCookie(w, s.cookie("", -1))
	return nil
}

// RequireAuth resolves the session cookie and puts the user in the request
// context. Browsers get sent to the login form, everything else gets a 401.
func (s *Service) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.user(r)
		if !ok {
			if r.Method != http.MethodGet {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/admin/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
	})
}

// RequireRole must be used inside RequireAuth.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, ok := UserFrom(r.Context())
			if !ok || !slices.Contains(roles, u.Role) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SameOriginPOST rejects state-changing requests carrying a foreign Origin.
// SameSite=Lax on the session cookie is the primary CSRF defence; this is a
// second line for the cases it does not cover, and needs no token plumbing.
func SameOriginPOST(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" {
				if u, err := url.Parse(origin); err != nil || u.Host != r.Host {
					http.Error(w, "cross-origin request rejected", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

type userKey struct{}

func UserFrom(ctx context.Context) (db.User, bool) {
	u, ok := ctx.Value(userKey{}).(db.User)
	return u, ok
}

// EnsureRoot creates or repairs the owner account from the environment. Setting
// KAKU_ROOT_PASSWORD to a new value and restarting is the password recovery
// path, so an existing root password is overwritten to match.
func EnsureRoot(ctx context.Context, q *db.Queries, cfg config.Config) error {
	if cfg.RootEmail == "" || cfg.RootPassword == "" {
		n, err := q.CountUsers(ctx)
		if err != nil {
			return fmt.Errorf("count users: %w", err)
		}
		if n == 0 {
			return errors.New("database has no users: set KAKU_ROOT_EMAIL and KAKU_ROOT_PASSWORD to create the owner account")
		}
		return nil
	}

	u, err := q.GetUserByEmail(ctx, cfg.RootEmail)
	if errors.Is(err, sql.ErrNoRows) {
		hash, err := HashPassword(cfg.RootPassword)
		if err != nil {
			return err
		}
		if _, err := q.CreateUser(ctx, db.CreateUserParams{
			Email: cfg.RootEmail, PasswordHash: hash, Name: "Owner", Role: RoleOwner,
		}); err != nil {
			return fmt.Errorf("create root user: %w", err)
		}
		slog.Info("created root user", "email", cfg.RootEmail)
		return nil
	}
	if err != nil {
		return fmt.Errorf("lookup root user: %w", err)
	}
	if CheckPassword(u.PasswordHash, cfg.RootPassword) {
		return nil
	}
	hash, err := HashPassword(cfg.RootPassword)
	if err != nil {
		return err
	}
	if err := q.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{PasswordHash: hash, ID: u.ID}); err != nil {
		return fmt.Errorf("update root password: %w", err)
	}
	if err := q.DeleteUserSessions(ctx, u.ID); err != nil {
		return fmt.Errorf("delete root sessions: %w", err)
	}
	slog.Info("updated root password from the environment", "email", cfg.RootEmail)
	return nil
}

func (s *Service) user(r *http.Request) (db.User, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return db.User{}, false
	}
	u, err := s.q.GetSessionUser(r.Context(), hashToken(c.Value))
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Error("session lookup", "err", err)
		}
		return db.User{}, false
	}
	return u, true
}

func (s *Service) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
	}
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Only the hash of the cookie value is stored, so a leaked database yields no
// usable session cookies.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
