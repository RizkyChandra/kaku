-- Keep comments in this directory ASCII-only; see revisions.sql for why.

-- The argument is an FTS5 query expression, not a literal string: escapeFTS in
-- internal/admin/search.go quotes the user's words before they reach it.
-- Written as the table-valued-function form posts_fts(?) rather than
-- "WHERE posts_fts MATCH ?" because sqlc's SQLite parser resolves the bare
-- table name on the left of MATCH as a column and rejects it. The two are the
-- same query to FTS5.
--
-- An empty lang means every language, the same contract the content API uses.
--
-- snippet() returns the matching text with the match wrapped in the delimiters
-- given here. Those delimiters are deliberately not HTML: the surrounding text
-- is post content and must be escaped before display, so the caller swaps the
-- markers for markup after escaping.
--
-- The last argument counts tokens, and a trigram token advances one character,
-- so 64, the most FTS5 accepts, is a window of about 64 characters where the
-- same number meant 64 words under unicode61. Anything smaller cuts the match
-- off mid-sentence.
-- name: SearchPosts :many
SELECT
    p.id, p.title, p.status, p.excerpt,
    u.name AS author_name,
    snippet(posts_fts, 1, '[[hl]]', '[[/hl]]', '...', 64) AS snippet
FROM posts_fts(sqlc.arg(query))
JOIN posts p ON p.id = posts_fts.rowid
JOIN users u ON u.id = p.author_id
WHERE (sqlc.arg(lang) = '' OR p.lang = sqlc.arg(lang))
ORDER BY rank
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- Counts what SearchPosts lists. It joins posts only so the language filter can
-- apply here too: a filtered list with an unfiltered total pages off the end.
-- name: CountSearchPosts :one
SELECT count(*)
FROM posts_fts(sqlc.arg(query))
JOIN posts p ON p.id = posts_fts.rowid
WHERE (sqlc.arg(lang) = '' OR p.lang = sqlc.arg(lang));

-- Fallback for queries too short to make a trigram. instr() rather than LIKE:
-- the needle is what the user typed, and instr has no wildcards to escape.
-- lower() on both sides gives the case-insensitivity the tokenizer would have,
-- for ASCII at least, which is all SQLite's lower() knows.
-- ponytail: full table scan over markdown. Fine at CMS scale; if it ever hurts,
-- narrow it to title or add a second index tokenized for short queries.
-- name: SearchPostsShort :many
SELECT p.id, p.title, p.status, p.excerpt, u.name AS author_name
FROM posts p
JOIN users u ON u.id = p.author_id
WHERE (instr(lower(p.title), lower(sqlc.arg(needle))) > 0
    OR instr(lower(p.markdown), lower(sqlc.arg(needle))) > 0)
  AND (sqlc.arg(lang) = '' OR p.lang = sqlc.arg(lang))
ORDER BY p.id DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountSearchPostsShort :one
SELECT count(*)
FROM posts p
WHERE (instr(lower(p.title), lower(sqlc.arg(needle))) > 0
    OR instr(lower(p.markdown), lower(sqlc.arg(needle))) > 0)
  AND (sqlc.arg(lang) = '' OR p.lang = sqlc.arg(lang));
