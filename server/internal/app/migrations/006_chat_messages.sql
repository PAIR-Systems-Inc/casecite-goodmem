CREATE TABLE cc_chat_messages (
    sequence bigserial PRIMARY KEY,
    id text NOT NULL UNIQUE,
    session_kind text NOT NULL DEFAULT 'chat_session' CHECK (session_kind = 'chat_session'),
    session_id text NOT NULL,
    data jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (session_kind,session_id) REFERENCES cc_objects(kind,id) ON DELETE CASCADE
);
CREATE INDEX cc_chat_messages_session ON cc_chat_messages(session_id,sequence);
