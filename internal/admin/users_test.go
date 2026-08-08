package admin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/RizkyChandra/kaku/internal/auth"
	"github.com/RizkyChandra/kaku/internal/config"
	"github.com/RizkyChandra/kaku/internal/db"
)

const userPassword = "correct horse"

func userHandler(t *testing.T) (*Handler, *db.Queries) {
	t.Helper()
	sqlDB, err := db.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	q := db.New(sqlDB)
	return New(q, auth.New(q, false), nil, nil, config.Config{}), q
}

// userMake inserts an account with userPassword as its password.
func userMake(t *testing.T, q *db.Queries, email, role string) db.User {
	t.Helper()
	hash, err := auth.HashPassword(userPassword)
	if err != nil {
		t.Fatal(err)
	}
	u, err := q.CreateUser(t.Context(), db.CreateUserParams{
		Email: email, PasswordHash: hash, Name: email, Role: role,
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func userLogin(t *testing.T, h *Handler, email string) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	if _, err := h.auth.Login(context.Background(), w, email, userPassword); err != nil {
		t.Fatal(err)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func userDo(t *testing.T, h *Handler, method, path string, form url.Values, c *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.AddCookie(c)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	return w
}

// The staff screen and its refusals must be readable to someone who cannot read
// English, since that is exactly where an admin gets stuck.
func TestUserStaffScreenInJapanese(t *testing.T) {
	h, q := userHandler(t)
	owner := userMake(t, q, "owner@example.com", auth.RoleOwner)
	c := userLogin(t, h, "owner@example.com")

	ja := func(method, path string, form url.Values) string {
		t.Helper()
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req := httptest.NewRequest(method, path, body)
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		req.Header.Set("Accept-Language", "ja")
		req.AddCookie(c)
		w := httptest.NewRecorder()
		h.Router().ServeHTTP(w, req)
		return w.Body.String()
	}

	list := ja("GET", "/users", nil)
	if !strings.Contains(list, "スタッフを追加") || !strings.Contains(list, "オーナー") {
		t.Error("staff list was not translated")
	}
	// The role value itself is protocol and must stay English.
	if !strings.Contains(list, `value="owner"`) && !strings.Contains(list, "owner@example.com") {
		t.Error("role values or emails were translated")
	}

	del := ja("POST", "/users/"+strconv.FormatInt(owner.ID, 10)+"/delete", url.Values{})
	if !strings.Contains(del, "これが最後のオーナーのため、削除できません。") {
		t.Errorf("last-owner refusal was not translated; body=%q", del)
	}
}

func TestUsersPaginates(t *testing.T) {
	h, q := userHandler(t)
	userMake(t, q, "owner@example.com", auth.RoleOwner)
	for i := range perPage + 5 {
		userMake(t, q, fmt.Sprintf("u%02d@example.com", i), auth.RoleAuthor)
	}
	c := userLogin(t, h, "owner@example.com")

	// ListUsers orders by name, which userMake sets to the email: owner sorts
	// first, so u00 is on page one and u24 is not.
	first := userDo(t, h, "GET", "/users", nil, c).Body.String()
	if !strings.Contains(first, "u00@example.com") || strings.Contains(first, "u24@example.com") {
		t.Fatal("page 1 is not the first page of staff")
	}
	if !strings.Contains(first, "?page=2") {
		t.Fatal("page 1 has no link to page 2")
	}

	second := userDo(t, h, "GET", "/users?page=2", nil, c).Body.String()
	if !strings.Contains(second, "u24@example.com") || strings.Contains(second, "u00@example.com") {
		t.Fatal("page 2 shows the same rows as page 1")
	}
}

func TestUsersClampsOutOfRangePage(t *testing.T) {
	h, q := userHandler(t)
	userMake(t, q, "owner@example.com", auth.RoleOwner)
	for i := range perPage + 5 {
		userMake(t, q, fmt.Sprintf("u%02d@example.com", i), auth.RoleAuthor)
	}
	c := userLogin(t, h, "owner@example.com")

	for _, path := range []string{"/users?page=99", "/users?page=0", "/users?page=-1", "/users?page=nope"} {
		w := userDo(t, h, "GET", path, nil, c)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, w.Code)
		}
		if !strings.Contains(w.Body.String(), "@example.com") {
			t.Fatalf("GET %s rendered no rows", path)
		}
	}
}

func TestUsersForbiddenToEditor(t *testing.T) {
	h, q := userHandler(t)
	userMake(t, q, "editor@example.com", auth.RoleEditor)
	c := userLogin(t, h, "editor@example.com")

	if got := userDo(t, h, "GET", "/users", nil, c).Code; got != http.StatusForbidden {
		t.Fatalf("GET /users as editor = %d, want 403", got)
	}
	// The editor still has a profile.
	if got := userDo(t, h, "GET", "/profile", nil, c).Code; got != http.StatusOK {
		t.Fatalf("GET /profile as editor = %d, want 200", got)
	}
}

func TestUserAdminCannotCreateOwner(t *testing.T) {
	h, q := userHandler(t)
	userMake(t, q, "owner@example.com", auth.RoleOwner)
	userMake(t, q, "admin@example.com", auth.RoleAdmin)
	c := userLogin(t, h, "admin@example.com")

	w := userDo(t, h, "POST", "/users", url.Values{
		"name": {"Sneaky"}, "email": {"sneaky@example.com"},
		"role": {auth.RoleOwner}, "password": {"longenough"},
	}, c)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("create owner as admin = %d, want 422", w.Code)
	}
	if n, err := q.CountUsersByRole(t.Context(), auth.RoleOwner); err != nil || n != 1 {
		t.Fatalf("owners = %d (err %v), want 1", n, err)
	}
}

func TestUserAdminCannotPromoteToOwner(t *testing.T) {
	h, q := userHandler(t)
	userMake(t, q, "owner@example.com", auth.RoleOwner)
	userMake(t, q, "admin@example.com", auth.RoleAdmin)
	author := userMake(t, q, "author@example.com", auth.RoleAuthor)
	c := userLogin(t, h, "admin@example.com")

	w := userDo(t, h, "POST", "/users/"+strconv.FormatInt(author.ID, 10), url.Values{
		"name": {"Author"}, "email": {author.Email}, "role": {auth.RoleOwner},
	}, c)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("promote to owner as admin = %d, want 422", w.Code)
	}
	if u, _ := q.GetUser(t.Context(), author.ID); u.Role != auth.RoleAuthor {
		t.Fatalf("role = %q, want author", u.Role)
	}
}

func TestUserAdminCannotEditOwner(t *testing.T) {
	h, q := userHandler(t)
	owner := userMake(t, q, "owner@example.com", auth.RoleOwner)
	userMake(t, q, "admin@example.com", auth.RoleAdmin)
	c := userLogin(t, h, "admin@example.com")

	for _, path := range []string{"", "/password", "/delete"} {
		w := userDo(t, h, "POST", "/users/"+strconv.FormatInt(owner.ID, 10)+path, url.Values{
			"name": {"x"}, "email": {owner.Email}, "role": {auth.RoleAdmin}, "password": {"longenough"},
		}, c)
		if w.Code != http.StatusForbidden {
			t.Fatalf("POST /users/{owner}%s as admin = %d, want 403", path, w.Code)
		}
	}
}

func TestUserLastOwnerCannotBeDeletedOrDemoted(t *testing.T) {
	h, q := userHandler(t)
	owner := userMake(t, q, "owner@example.com", auth.RoleOwner)
	c := userLogin(t, h, "owner@example.com")
	id := strconv.FormatInt(owner.ID, 10)

	w := userDo(t, h, "POST", "/users/"+id+"/delete", url.Values{}, c)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "last owner") {
		t.Fatalf("delete last owner = %d, want 422 explaining why", w.Code)
	}
	if _, err := q.GetUser(t.Context(), owner.ID); err != nil {
		t.Fatalf("last owner was deleted: %v", err)
	}

	w = userDo(t, h, "POST", "/users/"+id, url.Values{
		"name": {"Owner"}, "email": {owner.Email}, "role": {auth.RoleAdmin},
	}, c)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("demote last owner = %d, want 422", w.Code)
	}
	if u, _ := q.GetUser(t.Context(), owner.ID); u.Role != auth.RoleOwner {
		t.Fatalf("role = %q, want owner", u.Role)
	}
}

func TestUserCannotDeleteSelf(t *testing.T) {
	h, q := userHandler(t)
	userMake(t, q, "owner@example.com", auth.RoleOwner)
	admin := userMake(t, q, "admin@example.com", auth.RoleAdmin)
	c := userLogin(t, h, "admin@example.com")

	w := userDo(t, h, "POST", "/users/"+strconv.FormatInt(admin.ID, 10)+"/delete", url.Values{}, c)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("delete self = %d, want 422", w.Code)
	}
	if _, err := q.GetUser(t.Context(), admin.ID); err != nil {
		t.Fatalf("admin deleted themselves: %v", err)
	}
}

func TestUserDuplicateEmailIsAFieldError(t *testing.T) {
	h, q := userHandler(t)
	userMake(t, q, "owner@example.com", auth.RoleOwner)
	c := userLogin(t, h, "owner@example.com")

	w := userDo(t, h, "POST", "/users", url.Values{
		"name": {"Clone"}, "email": {"OWNER@example.com"},
		"role": {auth.RoleAuthor}, "password": {"longenough"},
	}, c)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "already belongs") {
		t.Fatalf("duplicate email = %d, want 422 with a message; body=%q", w.Code, w.Body.String())
	}
}

func TestUserOwnPasswordNeedsTheCurrentOne(t *testing.T) {
	h, q := userHandler(t)
	author := userMake(t, q, "author@example.com", auth.RoleAuthor)
	c := userLogin(t, h, "author@example.com")

	w := userDo(t, h, "POST", "/profile/password", url.Values{
		"current_password": {"not it"}, "password": {"a new password"},
	}, c)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong current password = %d, want 422", w.Code)
	}
	if u, _ := q.GetUser(t.Context(), author.ID); !auth.CheckPassword(u.PasswordHash, userPassword) {
		t.Fatal("password changed despite the wrong current password")
	}

	w = userDo(t, h, "POST", "/profile/password", url.Values{
		"current_password": {userPassword}, "password": {"a new password"},
	}, c)
	if w.Code != http.StatusOK {
		t.Fatalf("correct current password = %d, want 200", w.Code)
	}
	if u, _ := q.GetUser(t.Context(), author.ID); !auth.CheckPassword(u.PasswordHash, "a new password") {
		t.Fatal("password not changed")
	}
}

func TestUserAdminSetPasswordDropsSessions(t *testing.T) {
	h, q := userHandler(t)
	userMake(t, q, "owner@example.com", auth.RoleOwner)
	userMake(t, q, "admin@example.com", auth.RoleAdmin)
	author := userMake(t, q, "author@example.com", auth.RoleAuthor)
	authorCookie := userLogin(t, h, "author@example.com")
	adminCookie := userLogin(t, h, "admin@example.com")

	if got := userDo(t, h, "GET", "/profile", nil, authorCookie).Code; got != http.StatusOK {
		t.Fatalf("author profile before reset = %d, want 200", got)
	}
	w := userDo(t, h, "POST", "/users/"+strconv.FormatInt(author.ID, 10)+"/password",
		url.Values{"password": {"a new password"}}, adminCookie)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("set password = %d, want 303", w.Code)
	}
	// The old cookie must no longer resolve to a session.
	if got := userDo(t, h, "GET", "/profile", nil, authorCookie).Code; got != http.StatusSeeOther {
		t.Fatalf("author profile after reset = %d, want a redirect to login", got)
	}
	if u, _ := q.GetUser(t.Context(), author.ID); !auth.CheckPassword(u.PasswordHash, "a new password") {
		t.Fatal("password not changed")
	}
}

func TestUserShortPasswordRejected(t *testing.T) {
	h, q := userHandler(t)
	userMake(t, q, "owner@example.com", auth.RoleOwner)
	c := userLogin(t, h, "owner@example.com")

	w := userDo(t, h, "POST", "/users", url.Values{
		"name": {"Tiny"}, "email": {"tiny@example.com"},
		"role": {auth.RoleAuthor}, "password": {"short"},
	}, c)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("short password = %d, want 422", w.Code)
	}
}

func TestUserProfileCannotChangeOwnRole(t *testing.T) {
	h, q := userHandler(t)
	author := userMake(t, q, "author@example.com", auth.RoleAuthor)
	c := userLogin(t, h, "author@example.com")

	w := userDo(t, h, "POST", "/profile", url.Values{
		"name": {"Renamed"}, "role": {auth.RoleOwner}, "email": {"hijack@example.com"},
	}, c)
	if w.Code != http.StatusOK {
		t.Fatalf("profile update = %d, want 200", w.Code)
	}
	u, _ := q.GetUser(t.Context(), author.ID)
	if u.Role != auth.RoleAuthor || u.Email != "author@example.com" || u.Name != "Renamed" {
		t.Fatalf("profile update changed more than it should: %+v", u)
	}
}

func TestUserDeleteWithPostsIsRefused(t *testing.T) {
	h, q := userHandler(t)
	userMake(t, q, "owner@example.com", auth.RoleOwner)
	author := userMake(t, q, "author@example.com", auth.RoleAuthor)
	c := userLogin(t, h, "owner@example.com")

	if _, err := q.CreatePost(t.Context(), db.CreatePostParams{
		Uuid: "u1", Type: "post", Title: "T", Slug: "t", AuthorID: author.ID,
		Status: "draft", Visibility: "public",
	}); err != nil {
		t.Fatal(err)
	}

	w := userDo(t, h, "POST", "/users/"+strconv.FormatInt(author.ID, 10)+"/delete", url.Values{}, c)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "Reassign") {
		t.Fatalf("delete author with posts = %d, want 409 asking to reassign; body=%q", w.Code, w.Body.String())
	}
}
