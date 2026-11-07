-- Archive (operator console): hide a conversation from /conversations without deleting it. messages
-- and entity_sightings are append-only (NFR-8), so archiving is how test or simulator chats leave the
-- live list while staying reviewable on /archive. NULL = not archived.
ALTER TABLE conversations ADD COLUMN archived_at timestamptz;
