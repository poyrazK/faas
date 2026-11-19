package meter

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// partitionMaintenanceMigration returns the newest migration that defines
// monthly_partitions_drop_expired, where the partition retention windows live
// since meterd stopped running DDL itself (migration 20261118162630014).
func partitionMaintenanceMigration(t *testing.T) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	for i := len(files) - 1; i >= 0; i-- {
		body, err := os.ReadFile(files[i])
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "FUNCTION monthly_partitions_drop_expired(") {
			return string(body)
		}
	}
	t.Fatal("no migration defines monthly_partitions_drop_expired")
	return ""
}
