package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration191DefaultsPrioritySaturationWithoutConflictingWithAdvancedScheduler(t *testing.T) {
	content, err := FS.ReadFile("191_enable_priority_saturation_scheduler.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "'openai_priority_saturation_enabled'")
	require.Contains(t, sql, "key = 'openai_advanced_scheduler_enabled'")
	require.Contains(t, sql, "LOWER(TRIM(value)) = 'true'")
	require.Contains(t, sql, "THEN 'false' ELSE 'true'")
	require.Contains(t, sql, "ON CONFLICT (key) DO NOTHING")
}
