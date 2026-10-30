CREATE TABLE cc_connector_accounts (
    user_id text NOT NULL REFERENCES cc_users(id) ON DELETE CASCADE,
    provider text NOT NULL,
    generation text NOT NULL,
    credentials text NOT NULL,
    account jsonb NOT NULL DEFAULT '{}',
    last_sync timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, provider)
);
