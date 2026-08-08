-- Retokenise the full-text index as trigrams. unicode61 classes CJK ideographs
-- as alphanumeric and never segments them, so a space-free Japanese sentence
-- indexes as one enormous token and no part of it is searchable: "kaku" inside
-- "watashi wa mainichi kaku" matched nothing. Trigrams index every run of three
-- characters instead, which needs no per-language word breaker. The cost is
-- that queries shorter than three characters match no token at all; the handler
-- falls back to a LIKE scan for those.
--
-- posts_fts is an external-content index: it stores no text of its own, only
-- postings that point back at posts by rowid, so dropping it loses nothing that
-- 'rebuild' cannot regenerate. The three triggers are defined on posts, not on
-- posts_fts, so the drop leaves them in place and they keep firing against the
-- new table.
DROP TABLE posts_fts;

CREATE VIRTUAL TABLE posts_fts USING fts5(
    title,
    markdown,
    content='posts',
    content_rowid='id',
    tokenize='trigram'
);

INSERT INTO posts_fts (posts_fts) VALUES ('rebuild');
