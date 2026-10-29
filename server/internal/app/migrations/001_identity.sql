ALTER TABLE cc_users ADD COLUMN IF NOT EXISTS mfa_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE cc_users ADD COLUMN IF NOT EXISTS mfa_secret text NOT NULL DEFAULT '';
ALTER TABLE cc_users ADD COLUMN IF NOT EXISTS mfa_last_step bigint NOT NULL DEFAULT -1;
ALTER TABLE cc_users ADD COLUMN IF NOT EXISTS mfa_recovery jsonb NOT NULL DEFAULT '[]';
ALTER TABLE cc_users ADD COLUMN IF NOT EXISTS azure_oid text;
ALTER TABLE cc_users ADD COLUMN IF NOT EXISTS azure_tenant text;
ALTER TABLE cc_users ADD COLUMN IF NOT EXISTS company text NOT NULL DEFAULT '';
ALTER TABLE cc_users ADD COLUMN IF NOT EXISTS last_login timestamptz;
ALTER TABLE cc_users ADD COLUMN IF NOT EXISTS password_changed_at timestamptz NOT NULL DEFAULT now();
CREATE UNIQUE INDEX IF NOT EXISTS cc_users_azure_identity ON cc_users(azure_tenant,azure_oid) WHERE azure_oid IS NOT NULL;
ALTER TABLE cc_sessions ADD COLUMN IF NOT EXISTS session_id text NOT NULL DEFAULT gen_random_uuid()::text;
ALTER TABLE cc_sessions ADD COLUMN IF NOT EXISTS refresh_hash text;
ALTER TABLE cc_sessions ADD COLUMN IF NOT EXISTS client text NOT NULL DEFAULT 'Web';
CREATE UNIQUE INDEX IF NOT EXISTS cc_sessions_id ON cc_sessions(session_id);
CREATE UNIQUE INDEX IF NOT EXISTS cc_sessions_refresh ON cc_sessions(refresh_hash) WHERE refresh_hash IS NOT NULL;
CREATE TABLE IF NOT EXISTS cc_refresh_used (
    token_hash text PRIMARY KEY, session_id text NOT NULL,
    expires_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS cc_refresh_expiration ON cc_refresh_used(expires_at);
CREATE TABLE IF NOT EXISTS cc_device_codes (
    device_hash text PRIMARY KEY, user_code_hash text NOT NULL UNIQUE,
    user_id text REFERENCES cc_users(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'pending',
    interval_seconds integer NOT NULL DEFAULT 5,
    next_poll_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL DEFAULT now()+interval '10 minutes'
);
CREATE INDEX IF NOT EXISTS cc_device_expiration ON cc_device_codes(expires_at);
