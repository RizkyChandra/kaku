package admin

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/RizkyChandra/kaku/internal/auth"
	"github.com/RizkyChandra/kaku/internal/db"
	"github.com/RizkyChandra/kaku/internal/web/view"
)

func (h *Handler) mountUsers(r chi.Router) {
	// Managing staff is owner/admin only. Everyone signed in has a profile.
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireRole(auth.RoleOwner, auth.RoleAdmin))
		r.Get("/users", h.usersList)
		r.Get("/users/new", h.userNew)
		r.Post("/users", h.userCreate)
		r.Get("/users/{id}", h.userEdit)
		r.Post("/users/{id}", h.userUpdate)
		r.Post("/users/{id}/password", h.userSetPassword)
		r.Post("/users/{id}/delete", h.userDelete)
	})

	r.Get("/profile", h.profile)
	r.Post("/profile", h.profileUpdate)
	r.Post("/profile/password", h.profilePassword)
}

// userAssignable is what the acting role may hand out. An admin cannot mint
// owners, which is what keeps the owner tier closed; the submitted role is
// always checked against this, never trusted from the form.
func userAssignable(actorRole string) []string {
	if actorRole == auth.RoleOwner {
		return []string{auth.RoleOwner, auth.RoleAdmin, auth.RoleEditor, auth.RoleAuthor, auth.RoleContributor}
	}
	return []string{auth.RoleAdmin, auth.RoleEditor, auth.RoleAuthor, auth.RoleContributor}
}

func (h *Handler) usersList(w http.ResponseWriter, r *http.Request) {
	h.renderUsers(w, r, http.StatusOK, "")
}

func (h *Handler) renderUsers(w http.ResponseWriter, r *http.Request, status int, errMsg string) {
	users, err := h.q.ListUsers(r.Context())
	if err != nil {
		userFail(w, r, "list users", err)
		return
	}
	renderStatus(w, r, status, view.Users(h.page(r, "Staff", "users"), users, errMsg))
}

func (h *Handler) userNew(w http.ResponseWriter, r *http.Request) {
	actor, _ := auth.UserFrom(r.Context())
	render(w, r, view.UserForm(h.page(r, "New staff member", "users"), view.UserFormData{
		Target: db.User{Role: auth.RoleAuthor},
		Roles:  userAssignable(actor.Role),
	}))
}

func (h *Handler) userCreate(w http.ResponseWriter, r *http.Request) {
	actor, _ := auth.UserFrom(r.Context())
	password := r.FormValue("password")
	f := view.UserFormData{
		Target: db.User{
			Name:  strings.TrimSpace(r.FormValue("name")),
			Email: strings.TrimSpace(r.FormValue("email")),
			Role:  r.FormValue("role"),
		},
		Roles: userAssignable(actor.Role),
	}

	switch {
	case f.Target.Name == "":
		f.Err = "Name is required."
	case f.Target.Email == "":
		f.Err = "Email is required."
	case !slices.Contains(f.Roles, f.Target.Role):
		f.Err = "You are not allowed to assign that role."
	case len(password) < 8:
		f.Err = "Password must be at least 8 characters."
	default:
		taken, err := h.q.EmailExists(r.Context(), f.Target.Email)
		if err != nil {
			userFail(w, r, "check email", err)
			return
		}
		if taken {
			f.Err = "That email already belongs to another account."
		}
	}
	if f.Err != "" {
		renderStatus(w, r, http.StatusUnprocessableEntity,
			view.UserForm(h.page(r, "New staff member", "users"), f))
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		userFail(w, r, "hash password", err)
		return
	}
	if _, err := h.q.CreateUser(r.Context(), db.CreateUserParams{
		Email: f.Target.Email, PasswordHash: hash, Name: f.Target.Name, Role: f.Target.Role,
	}); err != nil {
		userFail(w, r, "create user", err)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (h *Handler) userEdit(w http.ResponseWriter, r *http.Request) {
	actor, target, ok := h.userTarget(w, r)
	if !ok {
		return
	}
	render(w, r, view.UserForm(h.page(r, target.Name, "users"), view.UserFormData{
		Target: target,
		Roles:  userAssignable(actor.Role),
	}))
}

func (h *Handler) userUpdate(w http.ResponseWriter, r *http.Request) {
	actor, target, ok := h.userTarget(w, r)
	if !ok {
		return
	}
	f := view.UserFormData{
		Target: db.User{
			ID:       target.ID,
			Name:     strings.TrimSpace(r.FormValue("name")),
			Email:    strings.TrimSpace(r.FormValue("email")),
			Role:     r.FormValue("role"),
			Bio:      strings.TrimSpace(r.FormValue("bio")),
			ImageUrl: strings.TrimSpace(r.FormValue("image_url")),
		},
		Roles: userAssignable(actor.Role),
	}

	switch {
	case f.Target.Name == "":
		f.Err = "Name is required."
	case f.Target.Email == "":
		f.Err = "Email is required."
	case !slices.Contains(f.Roles, f.Target.Role):
		f.Err = "You are not allowed to assign that role."
	default:
		if target.Role == auth.RoleOwner && f.Target.Role != auth.RoleOwner {
			last, err := h.lastOwner(r)
			if err != nil {
				userFail(w, r, "count owners", err)
				return
			}
			if last {
				f.Err = "This is the last owner. Promote someone else to owner first."
				break
			}
		}
		taken, err := h.q.EmailExistsExcept(r.Context(), db.EmailExistsExceptParams{Email: f.Target.Email, ID: target.ID})
		if err != nil {
			userFail(w, r, "check email", err)
			return
		}
		if taken {
			f.Err = "That email already belongs to another account."
		}
	}
	if f.Err != "" {
		renderStatus(w, r, http.StatusUnprocessableEntity,
			view.UserForm(h.page(r, target.Name, "users"), f))
		return
	}

	if _, err := h.q.UpdateUser(r.Context(), db.UpdateUserParams{
		Email: f.Target.Email, Name: f.Target.Name, Role: f.Target.Role,
		Bio: f.Target.Bio, ImageUrl: f.Target.ImageUrl, ID: target.ID,
	}); err != nil {
		userFail(w, r, "update user", err)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (h *Handler) userSetPassword(w http.ResponseWriter, r *http.Request) {
	actor, target, ok := h.userTarget(w, r)
	if !ok {
		return
	}
	password := r.FormValue("password")
	f := view.UserFormData{Target: target, Roles: userAssignable(actor.Role)}
	switch {
	case target.ID == actor.ID:
		// Changing your own password must prove you know the current one, and
		// that flow lives on the profile page.
		f.Err = "Change your own password from your profile."
	case len(password) < 8:
		f.Err = "Password must be at least 8 characters."
	}
	if f.Err != "" {
		renderStatus(w, r, http.StatusUnprocessableEntity,
			view.UserForm(h.page(r, target.Name, "users"), f))
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		userFail(w, r, "hash password", err)
		return
	}
	if err := h.q.UpdateUserPassword(r.Context(), db.UpdateUserPasswordParams{PasswordHash: hash, ID: target.ID}); err != nil {
		userFail(w, r, "update password", err)
		return
	}
	// Their old cookies must stop working the moment someone else resets them.
	if err := h.q.DeleteUserSessions(r.Context(), target.ID); err != nil {
		userFail(w, r, "delete sessions", err)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (h *Handler) userDelete(w http.ResponseWriter, r *http.Request) {
	actor, target, ok := h.userTarget(w, r)
	if !ok {
		return
	}
	if target.Role == auth.RoleOwner {
		last, err := h.lastOwner(r)
		if err != nil {
			userFail(w, r, "count owners", err)
			return
		}
		if last {
			h.renderUsers(w, r, http.StatusUnprocessableEntity, "This is the last owner and cannot be deleted.")
			return
		}
	}
	if target.ID == actor.ID {
		h.renderUsers(w, r, http.StatusUnprocessableEntity, "You cannot delete your own account.")
		return
	}

	if err := h.q.DeleteUser(r.Context(), target.ID); err != nil {
		// posts, media and api_keys reference users without a cascade, so an
		// account with any of them cannot be removed until they are reassigned.
		if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
			h.renderUsers(w, r, http.StatusConflict,
				target.Name+" still owns posts, uploads or API keys. Reassign their posts first.")
			return
		}
		userFail(w, r, "delete user", err)
		return
	}
	// The sessions row cascades with the user; deleting explicitly keeps the
	// cookies dead even if the foreign_keys pragma is ever off.
	if err := h.q.DeleteUserSessions(r.Context(), target.ID); err != nil {
		userFail(w, r, "delete sessions", err)
		return
	}
	h.renderUsers(w, r, http.StatusOK, "")
}

func (h *Handler) profile(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	render(w, r, view.Profile(h.page(r, "Profile", ""), u, "", ""))
}

func (h *Handler) profileUpdate(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	// Email and role are deliberately not editable here: they are the fields a
	// privilege check depends on.
	u.Name = strings.TrimSpace(r.FormValue("name"))
	u.Bio = strings.TrimSpace(r.FormValue("bio"))
	u.ImageUrl = strings.TrimSpace(r.FormValue("image_url"))
	if u.Name == "" {
		h.renderProfile(w, r, http.StatusUnprocessableEntity, u, "Name is required.", "")
		return
	}

	updated, err := h.q.UpdateUser(r.Context(), db.UpdateUserParams{
		Email: u.Email, Name: u.Name, Role: u.Role, Bio: u.Bio, ImageUrl: u.ImageUrl, ID: u.ID,
	})
	if err != nil {
		userFail(w, r, "update profile", err)
		return
	}
	h.renderProfile(w, r, http.StatusOK, updated, "", "Profile saved.")
}

func (h *Handler) profilePassword(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	password := r.FormValue("password")
	var errMsg string
	switch {
	case !auth.CheckPassword(u.PasswordHash, r.FormValue("current_password")):
		errMsg = "That is not your current password."
	case len(password) < 8:
		errMsg = "New password must be at least 8 characters."
	}
	if errMsg != "" {
		h.renderProfile(w, r, http.StatusUnprocessableEntity, u, errMsg, "")
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		userFail(w, r, "hash password", err)
		return
	}
	if err := h.q.UpdateUserPassword(r.Context(), db.UpdateUserPasswordParams{PasswordHash: hash, ID: u.ID}); err != nil {
		userFail(w, r, "update password", err)
		return
	}
	h.renderProfile(w, r, http.StatusOK, u, "", "Password changed.")
}

func (h *Handler) renderProfile(w http.ResponseWriter, r *http.Request, status int, u db.User, errMsg, okMsg string) {
	p := h.page(r, "Profile", "")
	p.User = &u // so the sidebar shows the name that was just saved
	renderStatus(w, r, status, view.Profile(p, u, errMsg, okMsg))
}

// userTarget resolves {id} and enforces the one rule every staff route shares:
// only an owner may touch another owner.
func (h *Handler) userTarget(w http.ResponseWriter, r *http.Request) (actor, target db.User, ok bool) {
	actor, _ = auth.UserFrom(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return actor, target, false
	}
	target, err = h.q.GetUser(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return actor, target, false
		}
		userFail(w, r, "get user", err)
		return actor, target, false
	}
	if target.Role == auth.RoleOwner && actor.Role != auth.RoleOwner {
		http.Error(w, "forbidden", http.StatusForbidden)
		return actor, target, false
	}
	return actor, target, true
}

func (h *Handler) lastOwner(r *http.Request) (bool, error) {
	n, err := h.q.CountUsersByRole(r.Context(), auth.RoleOwner)
	return n <= 1, err
}

// userFail logs and shows a plain 500: once a query fails there is nothing
// useful left to render.
func userFail(w http.ResponseWriter, r *http.Request, msg string, err error) {
	slog.ErrorContext(r.Context(), msg, "path", r.URL.Path, "err", err)
	http.Error(w, "something went wrong", http.StatusInternalServerError)
}
