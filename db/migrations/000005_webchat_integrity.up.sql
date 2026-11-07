-- Web chat channel (TR-22, TRD §4 design notes) and integrity checks for columns that repeat
-- facts stored elsewhere, so the copies cannot disagree.

-- ─────────────── Channels: web chat fallback / test channel ───────────────

-- The new value cannot be used inside this migration's transaction; nothing below does.
ALTER TYPE channel ADD VALUE IF NOT EXISTS 'webchat';

-- ─────────────── Messages: direction follows sender_type ───────────────

-- scammer → inbound; persona/operator → outbound. Operator messages name the operator, others do not.
ALTER TABLE messages
    ADD CONSTRAINT messages_direction_sender_ck
        CHECK ((sender_type = 'scammer') = (direction = 'inbound')),
    ADD CONSTRAINT messages_operator_id_ck
        CHECK ((sender_type = 'operator') = (operator_id IS NOT NULL));

-- ─────────────── Sightings: conversation_id must be the message's conversation ───────────────

ALTER TABLE messages ADD CONSTRAINT messages_id_conversation_uq UNIQUE (id, conversation_id);
ALTER TABLE entity_sightings
    ADD CONSTRAINT entity_sightings_message_conversation_fk
        FOREIGN KEY (message_id, conversation_id) REFERENCES messages (id, conversation_id);

-- ─────────────── Lookup logs: customer_id must own the api key ───────────────

ALTER TABLE api_keys ADD CONSTRAINT api_keys_id_customer_uq UNIQUE (id, customer_id);
ALTER TABLE lookup_logs
    ADD CONSTRAINT lookup_logs_api_key_customer_fk
        FOREIGN KEY (api_key_id, customer_id) REFERENCES api_keys (id, customer_id);
