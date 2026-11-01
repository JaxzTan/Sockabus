-- Read-only pre-check before applying migration 000005 (make db-check). Changes nothing: the whole
-- script runs in a READ ONLY transaction that is rolled back. Every "bad_rows" must be 0, or
-- 000005's new constraints fail and the migration stops dirty.
BEGIN READ ONLY;

SELECT version AS applied_migration, dirty FROM schema_migrations;

SELECT 'messages: direction must follow sender_type' AS check, count(*) AS bad_rows
  FROM messages WHERE (sender_type = 'scammer') <> (direction = 'inbound')
UNION ALL
SELECT 'messages: operator_id set exactly on operator messages', count(*)
  FROM messages WHERE (sender_type = 'operator') <> (operator_id IS NOT NULL)
UNION ALL
SELECT 'entity_sightings: conversation_id must be the message''s', count(*)
  FROM entity_sightings s JOIN messages m ON m.id = s.message_id
 WHERE m.conversation_id <> s.conversation_id
UNION ALL
SELECT 'lookup_logs: customer_id must own the api key', count(*)
  FROM lookup_logs l JOIN api_keys k ON k.id = l.api_key_id
 WHERE k.customer_id <> l.customer_id;

ROLLBACK;
