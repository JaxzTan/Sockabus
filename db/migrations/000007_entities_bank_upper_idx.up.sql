-- bank_or_chain is written in upper case from now on (MBB, TRON, TELEGRAM...). Older rows may be
-- lower case ('telegram', 'tron'); they are NOT rewritten. Lookups and the entity upsert compare
-- upper(bank_or_chain), and this expression index serves that comparison.
-- Additive: one new index, no data change.
CREATE INDEX entities_type_value_bank_upper_idx
    ON entities (type, value_normalized, upper(bank_or_chain));
