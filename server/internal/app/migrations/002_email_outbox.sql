CREATE TABLE cc_outbox (
    id text PRIMARY KEY, payload text NOT NULL, status text NOT NULL DEFAULT 'pending',
    attempts integer NOT NULL DEFAULT 0, next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_until timestamptz, error text,
    created_at timestamptz NOT NULL DEFAULT now(), sent_at timestamptz
);
CREATE INDEX cc_outbox_pending ON cc_outbox(next_attempt_at) WHERE status IN ('pending','sending');
