BEGIN;

CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Targeted indexes for report list/count filters.
CREATE INDEX IF NOT EXISTS jobs_report_tenant_created_idx
  ON jobs (tenant_id, created_at DESC)
  WHERE kind = 'report';

CREATE INDEX IF NOT EXISTS jobs_report_created_idx
  ON jobs (created_at DESC)
  WHERE kind = 'report';

CREATE INDEX IF NOT EXISTS jobs_report_status_created_idx
  ON jobs (tenant_id, status, created_at DESC)
  WHERE kind = 'report';

CREATE INDEX IF NOT EXISTS jobs_report_payload_trgm_idx
  ON jobs USING gin ((payload::text) gin_trgm_ops)
  WHERE kind = 'report';

COMMIT;
