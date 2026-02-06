BEGIN;

CREATE INDEX IF NOT EXISTS conversations_tenant_updated_idx
  ON conversations (tenant_id, updated_at DESC);

CREATE INDEX IF NOT EXISTS messages_conversation_created_idx
  ON messages (tenant_id, conversation_id, created_at DESC);

CREATE UNIQUE INDEX IF NOT EXISTS messages_dedupe_idx
  ON messages (tenant_id, conversation_id, dedupe_key);

CREATE INDEX IF NOT EXISTS messages_embedding_idx
  ON messages USING ivfflat (message_embedding vector_cosine_ops)
  WITH (lists = 100);

CREATE INDEX IF NOT EXISTS jobs_id_idx
  ON jobs (id);

CREATE INDEX IF NOT EXISTS jobs_status_idx
  ON jobs (tenant_id, status, updated_at DESC);

CREATE INDEX IF NOT EXISTS reports_job_idx
  ON reports (job_id, created_at DESC);

CREATE INDEX IF NOT EXISTS summaries_job_idx
  ON summaries (job_id, created_at DESC);

COMMIT;
