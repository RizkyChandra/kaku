-- published_at is the only timestamp Go writes. It goes through strftime so the
-- stored text matches the schema's own format; binding a time.Time directly
-- produces Go's String() layout, which breaks ordering and date comparisons.

-- name: CreatePost :one
INSERT INTO posts (
    uuid, type, title, slug, markdown, html, excerpt, feature_image,
    status, visibility, author_id, published_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', sqlc.arg(published_at))
)
RETURNING *;

-- name: UpdatePost :one
UPDATE posts SET
    title = ?, slug = ?, markdown = ?, html = ?, excerpt = ?, feature_image = ?,
    status = ?, visibility = ?,
    published_at = strftime('%Y-%m-%dT%H:%M:%fZ', sqlc.arg(published_at)),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ?
RETURNING *;

-- name: GetPost :one
SELECT * FROM posts WHERE id = ?;

-- name: GetPostBySlug :one
SELECT * FROM posts WHERE slug = ?;

-- name: DeletePost :exec
DELETE FROM posts WHERE id = ?;

-- name: ListPosts :many
SELECT p.*, u.name AS author_name
FROM posts p JOIN users u ON u.id = p.author_id
WHERE p.type = ?
ORDER BY COALESCE(p.published_at, p.updated_at) DESC
LIMIT ? OFFSET ?;

-- name: ListPostsByStatus :many
SELECT p.*, u.name AS author_name
FROM posts p JOIN users u ON u.id = p.author_id
WHERE p.type = ? AND p.status = ?
ORDER BY COALESCE(p.published_at, p.updated_at) DESC
LIMIT ? OFFSET ?;

-- name: CountPosts :one
SELECT count(*) FROM posts WHERE type = ?;

-- name: CountPostsByStatus :one
SELECT count(*) FROM posts WHERE type = ? AND status = ?;

-- name: PostSlugExists :one
SELECT EXISTS (SELECT 1 FROM posts WHERE slug = ?);

-- name: PostSlugExistsExcept :one
SELECT EXISTS (SELECT 1 FROM posts WHERE slug = ? AND id <> ?);

-- name: PublishDuePosts :execrows
UPDATE posts SET
    status = 'published',
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE status = 'scheduled'
  AND published_at IS NOT NULL
  AND published_at <= strftime('%Y-%m-%dT%H:%M:%fZ', 'now');

-- Public Content API: published, public posts only.

-- name: ListPublishedPosts :many
SELECT p.*, u.name AS author_name
FROM posts p JOIN users u ON u.id = p.author_id
WHERE p.type = ? AND p.status = 'published' AND p.visibility = 'public'
ORDER BY p.published_at DESC
LIMIT ? OFFSET ?;

-- name: CountPublishedPosts :one
SELECT count(*) FROM posts
WHERE type = ? AND status = 'published' AND visibility = 'public';

-- name: GetPublishedPostBySlug :one
SELECT p.*, u.name AS author_name
FROM posts p JOIN users u ON u.id = p.author_id
WHERE p.slug = ? AND p.status = 'published' AND p.visibility = 'public';

-- name: ListPublishedPostsByTag :many
SELECT p.*, u.name AS author_name
FROM posts p
JOIN users u ON u.id = p.author_id
JOIN post_tags pt ON pt.post_id = p.id
JOIN tags t ON t.id = pt.tag_id
WHERE t.slug = ? AND p.status = 'published' AND p.visibility = 'public'
ORDER BY p.published_at DESC
LIMIT ? OFFSET ?;
