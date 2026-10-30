CREATE TABLE cc_judge_cache (
    judge_id bigint PRIMARY KEY CHECK (judge_id > 0),
    generation text NOT NULL,
    profile jsonb,
    records jsonb NOT NULL DEFAULT '{}',
    metrics jsonb,
    build_job_id text REFERENCES cc_jobs(id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX cc_judge_build_job ON cc_judge_cache(build_job_id) WHERE build_job_id IS NOT NULL;
