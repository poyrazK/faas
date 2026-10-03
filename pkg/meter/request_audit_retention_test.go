// adr: 242
package meter

import (
	"context"
	"strings"
	"testing"
)

func TestRetentionOnceRequestAuditUsesBoundedThirtyDayDelete(t *testing.T) {
	db := &recordingExecer{rowsFn: func(i int) int64 {
		if i == 0 {
			return RetentionBatchSize
		}
		return 3
	}}
	rows, err := RetentionOnceRequestAudit(context.Background(), db)
	if err != nil || rows != RetentionBatchSize+3 {
		t.Fatalf("rows=%d err=%v", rows, err)
	}
	for _, call := range db.callsCopy() {
		if !strings.Contains(call.SQL, "request_audit_events") || !strings.Contains(call.SQL, "30 days") || !strings.Contains(call.SQL, "LIMIT $1") {
			t.Fatalf("unsafe audit retention SQL: %q", call.SQL)
		}
		if len(call.Args) != 1 || call.Args[0] != RetentionBatchSize {
			t.Fatalf("audit retention args: %+v", call.Args)
		}
	}
}
