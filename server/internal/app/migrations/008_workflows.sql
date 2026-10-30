CREATE TABLE cc_workflow_versions (
    workflow_kind text NOT NULL DEFAULT 'workflow' CHECK(workflow_kind='workflow'),
    workflow_id text NOT NULL,
    version integer NOT NULL CHECK(version>0),
    data jsonb NOT NULL,
    PRIMARY KEY(workflow_id,version),
    FOREIGN KEY(workflow_kind,workflow_id) REFERENCES cc_objects(kind,id) ON DELETE CASCADE
);
CREATE TABLE cc_workflow_runs (
    id text PRIMARY KEY,
    workflow_kind text NOT NULL DEFAULT 'workflow' CHECK(workflow_kind='workflow'),
    workflow_id text NOT NULL,
    runner_id text NOT NULL REFERENCES cc_users(id) ON DELETE CASCADE,
    job_id text UNIQUE REFERENCES cc_jobs(id) ON DELETE SET NULL,
    status text NOT NULL DEFAULT 'running',
    error text,
    data jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    FOREIGN KEY(workflow_kind,workflow_id) REFERENCES cc_objects(kind,id) ON DELETE CASCADE
);
CREATE INDEX cc_workflow_runs_workflow ON cc_workflow_runs(workflow_id,created_at DESC);
CREATE TABLE cc_workflow_steps (
    run_id text NOT NULL REFERENCES cc_workflow_runs(id) ON DELETE CASCADE,
    step_index integer NOT NULL CHECK(step_index>=0),
    status text NOT NULL DEFAULT 'pending',
    data jsonb NOT NULL,
    PRIMARY KEY(run_id,step_index)
);
CREATE TABLE cc_workflow_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id text NOT NULL REFERENCES cc_workflow_runs(id) ON DELETE CASCADE,
    event text NOT NULL,
    data jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz
);
CREATE INDEX cc_workflow_events_pending ON cc_workflow_events(id) WHERE processed_at IS NULL;

-- A worker crash, cancellation and timeout have exactly the same durable
-- lifecycle as a controlled step failure. Completed work remains available.
CREATE FUNCTION cc_workflow_job_status() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target text;
BEGIN
    IF NEW.kind <> 'workflow.run' OR NEW.status NOT IN ('completed','failed','cancelled') THEN RETURN NEW; END IF;
    SELECT id INTO target FROM cc_workflow_runs WHERE job_id=NEW.id AND status='running' FOR UPDATE;
    IF target IS NULL THEN RETURN NEW; END IF;
    IF NEW.status IN ('failed','cancelled') THEN
        UPDATE cc_workflow_steps SET status=CASE WHEN status='running' THEN 'failed' ELSE 'skipped' END,
            data=data || jsonb_build_object('error',CASE WHEN status='running' THEN NEW.error ELSE NULL END,'finished_at',NEW.updated_at)
            WHERE run_id=target AND status IN ('running','pending');
        UPDATE cc_workflow_runs SET status=NEW.status,error=NEW.error,completed_at=NEW.updated_at WHERE id=target;
    ELSIF NEW.result->>'status'='completed' THEN
        UPDATE cc_workflow_runs SET status='completed',error=NULL,completed_at=NEW.updated_at WHERE id=target;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER cc_workflow_job_status AFTER UPDATE OF status,result,error ON cc_jobs
FOR EACH ROW EXECUTE FUNCTION cc_workflow_job_status();

-- Snapshot the event at transition time. Mail delivery is retried separately
-- and cannot undo an approval or fail a model job.
CREATE FUNCTION cc_workflow_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status IS DISTINCT FROM OLD.status AND NEW.status IN ('completed','failed','awaiting_approval','rejected') THEN
        INSERT INTO cc_workflow_events(run_id,event,data) VALUES(NEW.id,NEW.status,
            jsonb_build_object('runner_id',NEW.runner_id,'workflow_id',NEW.workflow_id,'started_at',NEW.created_at,
            'error',NEW.error,'awaiting',NEW.data->'awaiting','decision_note',NEW.data->'decision_note'));
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER cc_workflow_event AFTER UPDATE OF status ON cc_workflow_runs
FOR EACH ROW EXECUTE FUNCTION cc_workflow_event();
