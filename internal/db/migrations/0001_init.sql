CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT NOT NULL,
    name          TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'editor', 'author', 'contributor')),
    bio           TEXT NOT NULL DEFAULT '',
    image_url     TEXT NOT NULL DEFAULT '',
    created_at    DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX sessions_user_id ON sessions (user_id);
CREATE INDEX sessions_expires_at ON sessions (expires_at);

CREATE TABLE posts (
    id            INTEGER PRIMARY KEY,
    uuid          TEXT NOT NULL UNIQUE,
    type          TEXT NOT NULL DEFAULT 'post' CHECK (type IN ('post', 'page')),
    title         TEXT NOT NULL,
    slug          TEXT NOT NULL UNIQUE,
    markdown      TEXT NOT NULL DEFAULT '',
    html          TEXT NOT NULL DEFAULT '',
    excerpt       TEXT NOT NULL DEFAULT '',
    feature_image TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'scheduled', 'published')),
    visibility    TEXT NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'private')),
    author_id     INTEGER NOT NULL REFERENCES users (id),
    published_at  DATETIME,
    created_at    DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX posts_status_published_at ON posts (status, published_at DESC);
CREATE INDEX posts_type ON posts (type);
CREATE INDEX posts_author_id ON posts (author_id);

CREATE TABLE post_revisions (
    id         INTEGER PRIMARY KEY,
    post_id    INTEGER NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
    title      TEXT NOT NULL,
    markdown   TEXT NOT NULL,
    author_id  INTEGER NOT NULL REFERENCES users (id),
    created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX post_revisions_post_id ON post_revisions (post_id, created_at DESC);

CREATE TABLE tags (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL,
    slug        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE post_tags (
    post_id  INTEGER NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
    tag_id   INTEGER NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    position INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (post_id, tag_id)
);

CREATE INDEX post_tags_tag_id ON post_tags (tag_id);

CREATE TABLE media (
    id          INTEGER PRIMARY KEY,
    key         TEXT NOT NULL UNIQUE,
    filename    TEXT NOT NULL,
    url         TEXT NOT NULL,
    mime        TEXT NOT NULL,
    size        INTEGER NOT NULL,
    uploaded_by INTEGER NOT NULL REFERENCES users (id),
    created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX media_created_at ON media (created_at DESC);

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE api_keys (
    id           INTEGER PRIMARY KEY,
    name         TEXT NOT NULL,
    key_hash     TEXT NOT NULL UNIQUE,
    created_by   INTEGER NOT NULL REFERENCES users (id),
    last_used_at DATETIME,
    created_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
