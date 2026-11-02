-- One open conversation per (honeypot account, scammer peer) (FR-2 routing race).
-- service.openConversation used to find-then-insert without a lock, so two first messages from
-- the same scammer arriving together could open two conversations. With this index the insert
-- uses ON CONFLICT DO NOTHING and re-selects, so concurrent first messages converge on one row.
--
-- Additive: a new index only. conversations_open_peer_idx (000004) stays. The table is small, so a
-- plain CREATE UNIQUE INDEX (brief write lock) is fine. It fails, changing nothing, if duplicate open
-- conversations already exist (check first: scripts/check-migrations.sql).
CREATE UNIQUE INDEX conversations_open_peer_uq
    ON conversations (account_id, scammer_peer_ref)
    WHERE status <> 'closed';
