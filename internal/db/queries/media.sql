-- name: CreateMedia :one
INSERT INTO media (key, filename, url, mime, size, uploaded_by, alt)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateMediaAlt :exec
UPDATE media SET alt = ? WHERE id = ?;

-- name: ListMedia :many
SELECT * FROM media ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: CountMedia :one
SELECT count(*) FROM media;

-- name: GetMedia :one
SELECT * FROM media WHERE id = ?;

-- name: DeleteMedia :exec
DELETE FROM media WHERE id = ?;
