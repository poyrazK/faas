package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/datarepair"
)

func TestDataRepairVerificationCLISeparatesCommitAndObservations(t *testing.T) {
	for _, hosted := range []bool{false, true} {
		for _, state := range []string{"unchanged", "edited", "missing", "purged", "invalid"} {
			id, digest := uuid.NewString(), strings.Repeat("a", 64)
			r := datarepair.VerificationReport{PlanID: id, PlanDigest: digest, Scope: datarepair.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"},
				SourceOperationID: uuid.NewString(), RepairOperationID: id, AppliedAt: time.Now(), CheckedAt: time.Now(), Restored: 459, Excluded: 41, Available: true, Checked: 459, Unchanged: 459,
				Issues: []string{}, Rows: []datarepair.ValidationRow{}}
			switch state {
			case "edited":
				r.Unchanged, r.Edited = 458, 1
				r.Rows = []datarepair.ValidationRow{{RowIdentity: uuid.NewString(), KeyJSON: `{"id": 9007199254740993}`, ExpectedVersion: "3", ObservedVersion: "4", Reason: datarepair.ReasonVersionChanged}}
			case "missing":
				r.Unchanged, r.Missing = 458, 1
				r.Rows = []datarepair.ValidationRow{{RowIdentity: uuid.NewString(), KeyJSON: `{"id": 42}`, ExpectedVersion: "3", Reason: datarepair.ReasonRecordMissing}}
			case "purged":
				r.Available, r.Checked, r.Unchanged, r.Issues = false, 0, 0, []string{"journal_purged"}
			case "invalid":
				r.PlanID = uuid.NewString()
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls++
				base := "/v1/data-repair/plans/"
				if hosted {
					base = "/v1/apps/products/data-repair/operations/"
				}
				var body map[string]any
				if req.Method != "POST" || req.URL.Path != base+id+"/verify" || req.Header.Get("Authorization") == "" ||
					json.NewDecoder(req.Body).Decode(&body) != nil || len(body) != 1 || body["digest"] != digest {
					t.Error("verification left its authenticated origin or supplied mutation fields")
				}
				_ = json.NewEncoder(w).Encode(r)
			}))
			args := []string{"verify", id, "--digest", digest}
			if hosted {
				t.Setenv("FAAS_API", server.URL)
				t.Setenv("FAAS_TOKEN", "verification-reader-token")
				args = append(args, "--app", "products")
			} else {
				t.Setenv("GREGALE_REPAIR_TOKEN", strings.Repeat("x", 32))
				args = append(args, "--url", server.URL)
			}
			var out bytes.Buffer
			old := (*main.OsStdout)
			(*main.OsStdout) = &out
			status := main.CmdDataRepair(args)
			(*main.OsStdout) = old
			server.Close()
			if calls != 1 || (status != 0) != (state == "purged" || state == "invalid") {
				t.Fatalf("hosted=%t state=%s status=%d requests=%d", hosted, state, status, calls)
			}
			if state != "invalid" {
				var got datarepair.VerificationReport
				if json.Unmarshal(out.Bytes(), &got) != nil || !got.Valid() || got.Restored != 459 || got.Excluded != 41 {
					t.Fatal("verification lost original receipt or unavailable JSON", out.String())
				}
			}
		}
	}
}
