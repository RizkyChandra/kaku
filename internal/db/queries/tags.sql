-- name: CreateTag :one
INSERT INTO tags (name, slug, description) VALUES (?, ?, ?) RETURNING *;

-- name: UpdateTag :one
UPDATE tags SET name = ?, slug = ?, description = ? WHERE id = ? RETURNING *;

-- name: DeleteTag :exec
DELETE FROM tags WHERE id = ?;

-- name: GetTag :one
SELECT * FROM tags WHERE id = ?;

-- name: GetTagBySlug :one
SELECT * FROM tags WHERE slug = ?;

-- name: ListTags :many
SELECT t.*, (SELECT count(*) FROM post_tags pt WHERE pt.tag_id = t.id) AS post_count
FROM tags t
ORDER BY t.name;

-- name: TagSlugExists :one
SELECT EXISTS (SELECT 1 FROM tags WHERE slug = ?);

-- name: TagSlugExistsExcept :one
SELECT EXISTS (SELECT 1 FROM tags WHERE slug = ? AND id <> ?);

-- name: ListPostTags :many
SELECT t.* FROM tags t
JOIN post_tags pt ON pt.tag_id = t.id
WHERE pt.post_id = ?
ORDER BY pt.position;

-- name: ClearPostTags :exec
DELETE FROM post_tags WHERE post_id = ?;

-- name: AddPostTag :exec
INSERT INTO post_tags (post_id, tag_id, position) VALUES (?, ?, ?)
ON CONFLICT (post_id, tag_id) DO UPDATE SET position = excluded.position;
