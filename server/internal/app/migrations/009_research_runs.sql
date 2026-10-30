CREATE TABLE cc_research_jobs (
    run_kind text NOT NULL DEFAULT 'research_run' CHECK(run_kind='research_run'),
    run_id text PRIMARY KEY,
    job_id text UNIQUE REFERENCES cc_jobs(id) ON DELETE SET NULL,
    FOREIGN KEY(run_kind,run_id) REFERENCES cc_objects(kind,id) ON DELETE CASCADE
);

-- Cancellation, timeout, lease expiry and completion update the saved run in
-- the queue transaction. Deleting a job later does not delete research history.
CREATE FUNCTION cc_research_job_status() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.kind='deep_research.run' AND NEW.status IN ('completed','failed','cancelled') THEN
        UPDATE cc_objects SET data=data || jsonb_build_object(
            'status',NEW.status,'result',CASE WHEN NEW.status='completed' THEN NEW.result ELSE NULL END,
            'error',NEW.error,'completed_at',NEW.updated_at),updated_at=NEW.updated_at
        WHERE kind='research_run' AND id=(SELECT run_id FROM cc_research_jobs WHERE job_id=NEW.id)
            AND data->>'status'='running';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER cc_research_job_status AFTER UPDATE OF status,result,error ON cc_jobs
FOR EACH ROW EXECUTE FUNCTION cc_research_job_status();
