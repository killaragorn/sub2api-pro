ALTER TABLE ops_error_logs
    ADD COLUMN IF NOT EXISTS is_sla_excluded BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS sla_exclusion_reason VARCHAR(128);

COMMENT ON COLUMN ops_error_logs.is_sla_excluded IS
    'Whether this error is excluded from SLA independently of business-limit classification';

COMMENT ON COLUMN ops_error_logs.sla_exclusion_reason IS
    'Stable reason code for SLA exclusion, for example prompt_guard_blocked';
