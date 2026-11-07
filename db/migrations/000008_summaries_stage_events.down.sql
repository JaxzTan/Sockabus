-- Drops only what 000008 created. The append-only triggers block DELETE/TRUNCATE, not DROP TABLE.
DROP TABLE IF EXISTS conversation_stage_events;
DROP TABLE IF EXISTS overview_summaries;
DROP TABLE IF EXISTS persona_summaries;
DROP TABLE IF EXISTS conversation_summaries;
