-- Keep comments in this directory ASCII-only; see revisions.sql for why.

-- The argument is an FTS5 query expression, not a literal string: escapeFTS in
-- internal/admin/search.go quotes the user's words before they reach it.
-- Written as the table-valued-function form posts_fts(?) rather than
-- "WHERE posts_fts MATCH ?" because sqlc's SQLite parser resolves the bare
-- table name on the left of MATCH as a column and rejects it. The two are the
-- same query to FTS5.
--
-- snippet() returns the matching text with the match wrapped in the delimiters
-- given here. Those delimiters are deliberately not HTML: the surrounding text
-- is post content and must be escaped before display, so the caller swaps the
-- markers for markup after escaping.
-- name: SearchPosts :many
SELECT
    p.id, p.title, p.status, p.excerpt,
    u.name AS author_name,
    snippet(posts_fts, 1, '[[hl]]', '[[/hl]]', '...', 24) AS snippet
FROM posts_fts(?)
JOIN posts p ON p.id = posts_fts.rowid
JOIN users u ON u.id = p.author_id
ORDER BY rank
LIMIT ? OFFSET ?;

-- name: CountSearchPosts :one
SELECT count(*) FROM posts_fts(?);
