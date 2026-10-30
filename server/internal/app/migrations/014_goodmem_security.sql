-- No FK cascades: retired identities and pending remote revocations must survive
-- deletion of the application account/document that originally created them.
CREATE TABLE cc_goodmem_subjects (
    principal_id text PRIMARY KEY, user_id text, credential text NOT NULL,
    retired boolean NOT NULL DEFAULT false, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX cc_goodmem_user ON cc_goodmem_subjects(user_id) WHERE NOT retired;
CREATE TABLE cc_goodmem_spaces (
    space_id text PRIMARY KEY, document_id text NOT NULL,
    dirty boolean NOT NULL DEFAULT true, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX cc_goodmem_document ON cc_goodmem_spaces(document_id);
CREATE TABLE cc_goodmem_access (
    space_id text NOT NULL REFERENCES cc_goodmem_spaces(space_id),
    principal_id text NOT NULL REFERENCES cc_goodmem_subjects(principal_id),
    assignment_id text NOT NULL UNIQUE, state text NOT NULL CHECK(state IN ('pending','active','revoking')),
    PRIMARY KEY(space_id,principal_id)
);
