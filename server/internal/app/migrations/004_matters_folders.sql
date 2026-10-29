ALTER TABLE cc_matters ADD COLUMN is_personal boolean NOT NULL DEFAULT false;
CREATE UNIQUE INDEX cc_matters_personal ON cc_matters(owner_id) WHERE is_personal;
CREATE TABLE cc_folders (
    owner_id text NOT NULL REFERENCES cc_users(id) ON DELETE CASCADE,
    path text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(owner_id,path)
);
