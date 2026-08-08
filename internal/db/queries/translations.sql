-- Keep comments in this directory ASCII-only; see revisions.sql for why.

-- Siblings of a post, itself included, so the editor can offer the languages
-- that already exist and the ones that do not.
--
-- The empty group is excluded deliberately. A row written without one would
-- otherwise be a sibling of every other such row, which silently links posts
-- that have nothing to do with each other.
-- name: ListTranslations :many
SELECT id, lang, slug, title, status, type
FROM posts
WHERE translation_group = ? AND translation_group <> ''
ORDER BY lang;

-- name: GetTranslation :one
SELECT * FROM posts WHERE translation_group = ? AND lang = ?;

-- name: SetTagTranslation :exec
INSERT INTO tag_translations (tag_id, lang, name, description) VALUES (?, ?, ?, ?)
ON CONFLICT (tag_id, lang) DO UPDATE
SET name = excluded.name, description = excluded.description;

-- name: DeleteTagTranslation :exec
DELETE FROM tag_translations WHERE tag_id = ? AND lang = ?;

-- name: ListTagTranslations :many
SELECT * FROM tag_translations WHERE tag_id = ? ORDER BY lang;

-- Tags labelled for one language, falling back to the tag's own name so an
-- untranslated tag still reads as something.
-- name: ListTagsLabelled :many
SELECT t.*, COALESCE(tt.name, t.name) AS label,
       (SELECT count(*) FROM post_tags pt WHERE pt.tag_id = t.id) AS post_count
FROM tags t
LEFT JOIN tag_translations tt ON tt.tag_id = t.id AND tt.lang = ?
ORDER BY label
LIMIT ? OFFSET ?;

-- name: ListPostTagsLabelled :many
SELECT t.*, COALESCE(tt.name, t.name) AS label
FROM tags t
JOIN post_tags pt ON pt.tag_id = t.id
LEFT JOIN tag_translations tt ON tt.tag_id = t.id AND tt.lang = ?
WHERE pt.post_id = ?
ORDER BY pt.position;
