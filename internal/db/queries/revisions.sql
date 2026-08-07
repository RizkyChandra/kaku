-- name: CreateRevision :exec
INSERT INTO post_revisions (post_id, title, markdown, author_id) VALUES (?, ?, ?, ?);

-- name: ListRevisions :many
SELECT r.id, r.post_id, r.title, r.created_at, u.name AS author_name
FROM post_revisions r JOIN users u ON u.id = r.author_id
WHERE r.post_id = ?
ORDER BY r.created_at DESC
LIMIT ?;

-- name: GetRevision :one
SELECT * FROM post_revisions WHERE id = ?;

-- Prunes a post's history. Ids are monotonic, so "older than the oldest one we
-- are keeping" is just an id comparison — sqlc's SQLite parser rejects the
-- self-referencing subquery a single-statement trim would need.
-- name: DeleteRevisionsBelowID :exec
DELETE FROM post_revisions WHERE post_id = ? AND id < ?;
