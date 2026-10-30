-- Derived OCR text only; originals, page rendering and searchable chunks remain
-- in GoodMem. A short renewable lease coalesces OCR across application instances.
CREATE TABLE cc_document_text (
    document_kind text NOT NULL DEFAULT 'document' CHECK(document_kind='document'),
    document_id text PRIMARY KEY,
    memory_id text NOT NULL,
    reader_version integer NOT NULL DEFAULT 1,
    data jsonb,
    lease_token text,
    lease_until timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY(document_kind,document_id) REFERENCES cc_objects(kind,id) ON DELETE CASCADE
);
