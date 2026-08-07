-- CreateSession expires the session 14 days from now. Expiry is computed in
-- SQL because the driver writes a bound time.Time in a format SQLite cannot
-- compare; keep it in step with auth.sessionLifetime, which sets Max-Age.
-- name: CreateSession :exec
INSERT INTO sessions (id, user_id, expires_at)
VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '+14 days'));

-- name: GetSessionUser :one
SELECT users.* FROM sessions
JOIN users ON users.id = sessions.user_id
WHERE sessions.id = ? AND sessions.expires_at > strftime('%Y-%m-%dT%H:%M:%fZ', 'now');

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = ?;

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= strftime('%Y-%m-%dT%H:%M:%fZ', 'now');
