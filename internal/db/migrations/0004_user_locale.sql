-- Per-user admin language. Empty means "follow the site setting", which keeps
-- existing accounts on whatever the operator chooses rather than pinning them
-- to English at upgrade time.
ALTER TABLE users ADD COLUMN locale TEXT NOT NULL DEFAULT '';
