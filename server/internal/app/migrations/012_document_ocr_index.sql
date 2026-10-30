-- The searchable OCR memory is published only with durable job completion.
-- Failure, cancellation and expired worker leases cannot make a partial scan
-- appear indexed. The original memory still owns file bytes and page images.
CREATE INDEX cc_documents_original_memory ON cc_objects((data->>'memory_id')) WHERE kind='document';
CREATE INDEX cc_documents_ocr_memory ON cc_objects((data->>'index_memory_id')) WHERE kind='document';

CREATE FUNCTION cc_document_ocr_status() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.kind='document.ocr_index' AND NEW.status IN ('completed','failed','cancelled') THEN
        UPDATE cc_objects SET data=data || jsonb_build_object(
            'status',CASE WHEN NEW.status='completed' THEN 'indexed' ELSE 'failed' END,
            'processing_status',CASE WHEN NEW.status='completed' THEN 'COMPLETED' ELSE 'FAILED' END,
            'error',NEW.error,
            'indexed_at',CASE WHEN NEW.status='completed' THEN NEW.updated_at ELSE NULL END),updated_at=NEW.updated_at
        WHERE kind='document' AND id=NEW.input->>'document_id'
            AND data->>'ingest_job_id'=NEW.id
            AND data->>'memory_id'=NEW.input->>'memory_id'
            AND data->>'index_memory_id'=NEW.input->>'index_memory_id';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER cc_document_ocr_status AFTER UPDATE OF status,result,error ON cc_jobs
FOR EACH ROW EXECUTE FUNCTION cc_document_ocr_status();
