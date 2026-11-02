ALTER TABLE lookup_logs DROP CONSTRAINT IF EXISTS lookup_logs_api_key_customer_fk;
ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_id_customer_uq;

ALTER TABLE entity_sightings DROP CONSTRAINT IF EXISTS entity_sightings_message_conversation_fk;
ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_id_conversation_uq;

ALTER TABLE messages
    DROP CONSTRAINT IF EXISTS messages_operator_id_ck,
    DROP CONSTRAINT IF EXISTS messages_direction_sender_ck;

-- 'webchat' stays in the channel enum: PostgreSQL cannot drop an enum value.
