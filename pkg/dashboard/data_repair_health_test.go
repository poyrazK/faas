package dashboard_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func TestAdminRepairHealthGuidanceAndUnavailable(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "cleanup", true: "read unavailable"}[unavailable], func(t *testing.T) {
			data := dashboard.AdminData{RepairHealth: &api.DataRepairHealth{
				ObservedAt: time.Now().UTC(), HeldSlots: 1, HeldMemoryMB: 520,
				Items:      []api.DataRepairHealthItem{{OperationID: "original-operation", TaskID: "original-task", NodeID: "original-node", Phase: "cleanup", AgeSeconds: 180, NeedsAttention: true, CapacityHeld: true, HeldMemoryMB: 520, HeldVCPUs: 1, CancelRequested: true, FailureCode: "repair_cleanup_unconfirmed", RecoveryGuidance: "Preserve <script>journals</script> and capacity"}},
				NextCursor: "t:030e2012-e15a-4205-9ede-d9796f2b7544",
			}}
			if unavailable {
				data.RepairHealthError = "Repair health is unavailable."
			}
			w := httptest.NewRecorder()
			if err := dashboard.Render(w, slog.New(slog.NewTextHandler(io.Discard, nil)), "", dashboard.Page{Title: "Admin", Body: "admin", Account: &dashboard.AccountView{Email: "ops@example.com"}, Data: data}); err != nil {
				t.Fatal(err)
			}
			body := w.Body.String()
			if unavailable {
				if !strings.Contains(body, data.RepairHealthError) || strings.Contains(body, "held slots") || strings.Contains(body, "No active repairs") {
					t.Fatal("unavailable read displayed healthy state")
				}
				return
			}
			for _, want := range []string{"original-task", "original-node", "original-operation", "520 MiB / 1 vCPU", "Needs attention", "Cancellation requested", "repair_cleanup_unconfirmed", "&lt;script&gt;journals&lt;/script&gt;", "Next repairs", "Recovery runbook"} {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			if strings.Contains(body, "<script>journals</script>") || strings.Contains(body, "<form") {
				t.Fatal("unsafe markup or mutation form in read-only view")
			}
		})
	}
}
