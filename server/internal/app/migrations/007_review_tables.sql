CREATE TABLE cc_review_cells (
    table_kind text NOT NULL DEFAULT 'review_table' CHECK (table_kind='review_table'),
    table_id text NOT NULL,
    document_id text NOT NULL,
    field_key text NOT NULL,
    data jsonb NOT NULL,
    PRIMARY KEY(table_id,document_id,field_key),
    FOREIGN KEY(table_kind,table_id) REFERENCES cc_objects(kind,id) ON DELETE CASCADE
);
CREATE TABLE cc_review_runs (
    id text PRIMARY KEY,
    table_kind text NOT NULL DEFAULT 'review_table' CHECK (table_kind='review_table'),
    table_id text NOT NULL,
    runner_id text NOT NULL REFERENCES cc_users(id) ON DELETE CASCADE,
    job_id text UNIQUE REFERENCES cc_jobs(id) ON DELETE SET NULL,
    scope jsonb NOT NULL,
    status text NOT NULL DEFAULT 'running',
    counts jsonb,
    error text,
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    FOREIGN KEY(table_kind,table_id) REFERENCES cc_objects(kind,id) ON DELETE CASCADE
);
CREATE INDEX cc_review_runs_table ON cc_review_runs(table_id,started_at DESC);

-- The durable queue owns lifecycle state. A crash, cancellation, or expired
-- lease updates run history in the very same transaction, without a second
-- worker or a process-local completion callback. History survives job retention.
CREATE FUNCTION cc_review_run_job_status() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.kind = 'review_table.run' THEN
        UPDATE cc_review_runs SET
            status = CASE WHEN NEW.status='pending' THEN 'running' ELSE NEW.status END,
            counts = NEW.result,
            error = NEW.error,
            finished_at = CASE WHEN NEW.status IN ('completed','failed','cancelled') THEN NEW.updated_at ELSE NULL END
        WHERE job_id=NEW.id;
        IF NEW.status='completed' AND OLD.status<>'completed' THEN
            UPDATE cc_objects SET data=jsonb_set(data,'{last_run_at}',to_jsonb(NEW.updated_at)),updated_at=NEW.updated_at
            WHERE kind='review_table' AND id=(SELECT table_id FROM cc_review_runs WHERE job_id=NEW.id);
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER cc_review_run_job_status AFTER UPDATE OF status,result,error ON cc_jobs
FOR EACH ROW EXECUTE FUNCTION cc_review_run_job_status();
