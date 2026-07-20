-- Persist a redacted inbound request snapshot for upstream cyber_policy hits.

ALTER TABLE content_moderation_logs
    ADD COLUMN IF NOT EXISTS cyber_request_protocol VARCHAR(32) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS cyber_request_snapshot TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS cyber_request_original_bytes BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cyber_request_stored_bytes INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cyber_request_truncated BOOLEAN NOT NULL DEFAULT FALSE;
