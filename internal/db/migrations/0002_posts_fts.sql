-- Full-text index over posts. External content: the index stores no copy of the
-- text, it reads columns back from posts by rowid, so the triggers below are the
-- only thing keeping the two in step.

CREATE VIRTUAL TABLE posts_fts USING fts5(
    title,
    markdown,
    content='posts',
    content_rowid='id',
    tokenize='unicode61'
);

INSERT INTO posts_fts (rowid, title, markdown)
SELECT id, title, markdown FROM posts;

CREATE TRIGGER posts_fts_insert AFTER INSERT ON posts BEGIN
    INSERT INTO posts_fts (rowid, title, markdown)
    VALUES (new.id, new.title, new.markdown);
END;

-- Deletes and updates must hand FTS5 the OLD values: it cannot read them back
-- from posts once the row has changed, and an index built from the wrong text
-- stays wrong forever.
CREATE TRIGGER posts_fts_delete AFTER DELETE ON posts BEGIN
    INSERT INTO posts_fts (posts_fts, rowid, title, markdown)
    VALUES ('delete', old.id, old.title, old.markdown);
END;

CREATE TRIGGER posts_fts_update AFTER UPDATE ON posts BEGIN
    INSERT INTO posts_fts (posts_fts, rowid, title, markdown)
    VALUES ('delete', old.id, old.title, old.markdown);
    INSERT INTO posts_fts (rowid, title, markdown)
    VALUES (new.id, new.title, new.markdown);
END;
