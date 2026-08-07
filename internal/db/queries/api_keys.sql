-- Only the SHA-256 of a Content API key is stored, so the plaintext is shown
-- once at creation and never again.

-- name: CreateApiKey :one
INSERT INTO api_keys (name, key_hash, created_by) VALUES (?, ?, ?) RETURNING *;

-- name: ListApiKeys :many
SELECT k.*, u.name AS created_by_name
FROM api_keys k JOIN users u ON u.id = k.created_by
ORDER BY k.created_at DESC;

-- name: GetApiKeyByHash :one
SELECT * FROM api_keys WHERE key_hash = ?;

-- name: TouchApiKey :exec
UPDATE api_keys SET last_used_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?;

-- name: DeleteApiKey :exec
DELETE FROM api_keys WHERE id = ?;
