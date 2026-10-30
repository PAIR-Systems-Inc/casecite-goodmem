CREATE TABLE IF NOT EXISTS cc_users (
    id text PRIMARY KEY, email text NOT NULL UNIQUE, name text NOT NULL,
    password_hash text NOT NULL, role text NOT NULL DEFAULT 'attorney',
    active boolean NOT NULL DEFAULT true, must_change_password boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS cc_sessions (
    token_hash text PRIMARY KEY, user_id text NOT NULL REFERENCES cc_users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(), last_activity timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL, ip_address text NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS cc_sessions_user ON cc_sessions(user_id);
CREATE TABLE IF NOT EXISTS cc_matters (
    id text PRIMARY KEY, owner_id text NOT NULL REFERENCES cc_users(id),
    name text NOT NULL, client_name text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS cc_matter_members (
    matter_id text NOT NULL REFERENCES cc_matters(id) ON DELETE CASCADE,
    user_id text NOT NULL REFERENCES cc_users(id) ON DELETE CASCADE,
    PRIMARY KEY(matter_id, user_id)
);
CREATE TABLE IF NOT EXISTS cc_objects (
    kind text NOT NULL, id text NOT NULL, owner_id text NOT NULL REFERENCES cc_users(id) ON DELETE CASCADE,
    matter_id text REFERENCES cc_matters(id) ON DELETE SET NULL,
    data jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(kind, id)
);
CREATE INDEX IF NOT EXISTS cc_objects_owner ON cc_objects(kind, owner_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS cc_objects_matter ON cc_objects(matter_id, kind);
CREATE TABLE IF NOT EXISTS cc_kv (
    key text PRIMARY KEY, value jsonb NOT NULL, expires_at timestamptz
);
CREATE INDEX IF NOT EXISTS cc_kv_expiration ON cc_kv(expires_at) WHERE expires_at IS NOT NULL;
CREATE TABLE IF NOT EXISTS cc_jobs (
    id text PRIMARY KEY, user_id text NOT NULL REFERENCES cc_users(id) ON DELETE CASCADE,
    kind text NOT NULL, input jsonb NOT NULL DEFAULT '{}', result jsonb,
    status text NOT NULL DEFAULT 'pending', error text,
    lease_token text, lease_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS cc_jobs_pending ON cc_jobs(created_at) WHERE status = 'pending';
CREATE TABLE IF NOT EXISTS cc_audit (
    id bigserial PRIMARY KEY, user_id text, event_type text NOT NULL,
    resource_type text NOT NULL DEFAULT '', resource_id text NOT NULL DEFAULT '',
    details jsonb NOT NULL DEFAULT '{}', created_at timestamptz NOT NULL DEFAULT now()
);
