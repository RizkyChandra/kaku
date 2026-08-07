-- Alt text for uploads, so an image inserted into a post can be described.
ALTER TABLE media ADD COLUMN alt TEXT NOT NULL DEFAULT '';
