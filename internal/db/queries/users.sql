-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: GetUser :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = ? COLLATE NOCASE;

-- name: CreateUser :one
INSERT INTO users (email, password_hash, name, role)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: UpdateUserPassword :exec
UPDATE users
SET password_hash = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ?;

-- name: ListUsers :many
SELECT * FROM users ORDER BY name LIMIT ? OFFSET ?;

-- name: UpdateUser :one
UPDATE users SET
    email = ?, name = ?, role = ?, bio = ?, image_url = ?,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ?
RETURNING *;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = ?;

-- name: CountUsersByRole :one
SELECT count(*) FROM users WHERE role = ?;

-- name: EmailExists :one
SELECT EXISTS (SELECT 1 FROM users WHERE email = ? COLLATE NOCASE);

-- name: EmailExistsExcept :one
SELECT EXISTS (SELECT 1 FROM users WHERE email = ? COLLATE NOCASE AND id <> ?);

-- name: UpdateUserLocale :exec
UPDATE users
SET locale = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ?;
