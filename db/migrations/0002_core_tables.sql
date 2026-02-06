BEGIN;

CREATE TABLE IF NOT EXISTS conversations (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT 'whatsapp_web',
  title TEXT NOT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Partition by tenant first and then by date to support high cardinality tenants.
CREATE TABLE IF NOT EXISTS messages (
  id UUID NOT NULL DEFAULT gen_random_uuid(),
  tenant_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  source_message_id TEXT,
  author_role TEXT NOT NULL,
  message_text TEXT NOT NULL,
  message_embedding VECTOR(1536),
  dedupe_key TEXT NOT NULL,
  checksum TEXT NOT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL,
  ingested_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (tenant_id, created_at, id)
) PARTITION BY LIST (tenant_id);

CREATE TABLE IF NOT EXISTS messages_tenant_default
PARTITION OF messages DEFAULT
PARTITION BY RANGE (created_at);

CREATE TABLE IF NOT EXISTS messages_tenant_default_2026
PARTITION OF messages_tenant_default
FOR VALUES FROM ('2026-01-01') TO ('2027-01-01');

CREATE TABLE IF NOT EXISTS messages_tenant_default_future
PARTITION OF messages_tenant_default
DEFAULT;

CREATE TABLE IF NOT EXISTS jobs (
  id UUID NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('summary', 'report')),
  tenant_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  payload JSONB NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending', 'processing', 'done', 'failed')),
  result JSONB,
  error_message TEXT,
  attempts INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, created_at, id)
) PARTITION BY LIST (tenant_id);

CREATE TABLE IF NOT EXISTS jobs_tenant_default
PARTITION OF jobs DEFAULT
PARTITION BY RANGE (created_at);

CREATE TABLE IF NOT EXISTS jobs_tenant_default_2026
PARTITION OF jobs_tenant_default
FOR VALUES FROM ('2026-01-01') TO ('2027-01-01');

CREATE TABLE IF NOT EXISTS jobs_tenant_default_future
PARTITION OF jobs_tenant_default
DEFAULT;

CREATE TABLE IF NOT EXISTS summaries (
  summary_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  job_id UUID NOT NULL,
  tenant_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  prompt_version TEXT NOT NULL,
  model_id TEXT NOT NULL,
  content JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS reports (
  report_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  job_id UUID NOT NULL,
  tenant_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  prompt_version TEXT NOT NULL,
  model_id TEXT NOT NULL,
  content JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMIT;
