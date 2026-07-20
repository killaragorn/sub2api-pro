package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCyberPolicyRequestAuditMigrationAddsBoundedSnapshotMetadata(t *testing.T) {
	content, err := FS.ReadFile("185_cyber_policy_request_audit.sql")
	require.NoError(t, err)

	sql := string(content)
	require.Contains(t, sql, "cyber_request_protocol")
	require.Contains(t, sql, "cyber_request_snapshot")
	require.Contains(t, sql, "cyber_request_original_bytes")
	require.Contains(t, sql, "cyber_request_stored_bytes")
	require.Contains(t, sql, "cyber_request_truncated")
	require.Contains(t, sql, "IF NOT EXISTS")
}
