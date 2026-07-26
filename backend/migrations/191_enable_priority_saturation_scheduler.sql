-- Enable deterministic priority-saturation scheduling by default. Preserve an
-- explicitly enabled weighted-topk scheduler so the two mutually exclusive
-- switches cannot both become true during upgrade.
INSERT INTO settings (key, value, updated_at)
SELECT
    'openai_priority_saturation_enabled',
    CASE
        WHEN EXISTS (
            SELECT 1
            FROM settings
            WHERE key = 'openai_advanced_scheduler_enabled'
              AND LOWER(TRIM(value)) = 'true'
        )
        THEN 'false'
        ELSE 'true'
    END,
    NOW()
ON CONFLICT (key) DO NOTHING;
