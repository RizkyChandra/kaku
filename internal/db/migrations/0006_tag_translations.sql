-- One tag, many labels. The tag itself keeps a single slug so that a URL like
-- ?tag=go returns posts in every language; only the display name and
-- description are per language. tags.name stays as the fallback label.
CREATE TABLE tag_translations (
    tag_id      INTEGER NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    lang        TEXT NOT NULL,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (tag_id, lang)
);
