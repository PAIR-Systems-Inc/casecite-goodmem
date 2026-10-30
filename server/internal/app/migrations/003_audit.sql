ALTER TABLE cc_audit ADD COLUMN record jsonb;
ALTER TABLE cc_audit ADD COLUMN entry_hash text;
ALTER TABLE cc_audit ADD COLUMN previous_hash text;
CREATE TABLE cc_audit_head (
    singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
    last_id bigint NOT NULL, entry_count bigint NOT NULL,
    entry_hash text NOT NULL, signature text NOT NULL
);
ALTER TABLE cc_outbox ADD COLUMN owner_id text REFERENCES cc_users(id) ON DELETE CASCADE;
