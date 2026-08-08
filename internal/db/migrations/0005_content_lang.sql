-- Each translation is its own post row, sharing a translation_group with its
-- siblings. Revisions, tags, status, scheduling and full-text search then work
-- per translation with no new join anywhere.
--
-- lang carries no CHECK constraint on purpose: the enabled set is a setting, so
-- adding a language must not require a migration. It is normalised in Go, the
-- same way status, type and visibility already are.
ALTER TABLE posts ADD COLUMN lang TEXT NOT NULL DEFAULT 'en';
ALTER TABLE posts ADD COLUMN translation_group TEXT NOT NULL DEFAULT '';

-- Existing posts each become a group of one, keyed on their own uuid, so the
-- "add a translation" path has something to attach to.
UPDATE posts SET translation_group = uuid WHERE translation_group = '';

CREATE INDEX posts_translation_group ON posts (translation_group, lang);
CREATE INDEX posts_lang ON posts (lang);
