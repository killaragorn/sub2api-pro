package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration193AddsIndependentOpsErrorSLAExclusion(t *testing.T) {
	content, err := FS.ReadFile("193_add_ops_error_sla_exclusion.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS is_sla_excluded BOOLEAN NOT NULL DEFAULT FALSE")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS sla_exclusion_reason VARCHAR(128)")
	require.NotContains(t, sql, "is_business_limited = TRUE")
}
