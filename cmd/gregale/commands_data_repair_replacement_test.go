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
	"github.com/onebox-faas/faas/pkg/api"
	repairapi "github.com/onebox-faas/faas/pkg/apid/datarepair"
	"github.com/onebox-faas/faas/pkg/datarepair"
)

func TestDataRepairReplacementCLIUsesSavedIdentity(t *testing.T) {
	for _, hosted := range []bool{false, true} {
		parent, id, digest := uuid.NewString(), uuid.NewString(), strings.Repeat("a", 64)
		child := datarepair.Plan{ID: id, SourceOperationID: uuid.NewString(), Digest: strings.Repeat("b", 64), Scope: datarepair.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"},
			Candidates: 1, Conflicts: 1, ExpiresAt: time.Now().Add(time.Minute),
			Replacement: &datarepair.Replacement{ParentPlanID: parent, ParentPlanDigest: digest, PreviousCandidates: 2, RetainedCandidates: 1, RemovedCandidates: 1},
			Items:       []datarepair.Item{{RowIdentity: uuid.NewString(), Status: "candidate", PreviousStatus: "candidate"}, {RowIdentity: uuid.NewString(), Status: "conflict", PreviousStatus: "candidate"}}}
		posts, gets := 0, 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			base := "/v1/data-repair/plans/"
			if hosted {
				base = "/v1/apps/products/data-repair/operations/"
			}
			if req.Header.Get("Authorization") == "" {
				t.Error("replacement omitted authentication")
			}
			if hosted && req.Method == "GET" && req.URL.Path == base+parent {
				gets++
				_ = json.NewEncoder(w).Encode(repairapi.ManagedResponse{Operation: api.OperationResponse{ID: parent, LatestSequence: 5}, Plan: datarepair.Plan{ID: parent, Digest: digest}})
				return
			}
			posts++
			if req.Method != "POST" || req.URL.Path != base+parent+"/replace" {
				t.Error("replacement left its locally constructed endpoint")
			}
			var body map[string]any
			if json.NewDecoder(req.Body).Decode(&body) != nil || body["digest"] != digest || len(body) != 2 {
				t.Error("replacement changed parent digest or supplied extra authority")
			}
			if hosted {
				if req.Header.Get("Idempotency-Key") != id || body["expected_sequence"] != float64(5) {
					t.Error("hosted replacement lost saved identity/CAS sequence")
				}
				_ = json.NewEncoder(w).Encode(repairapi.ManagedResponse{Operation: api.OperationResponse{ID: id}, Plan: child})
			} else {
				if body["plan_id"] != id {
					t.Error("standalone replacement lost saved identity")
				}
				_ = json.NewEncoder(w).Encode(child)
			}
		}))
		args := []string{"replace", parent, "--digest", digest, "--repair-operation", id, "--yes"}
		if hosted {
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "replacement-manager-token")
			args = append(args, "--app", "products")
		} else {
			t.Setenv("GREGALE_REPAIR_TOKEN", strings.Repeat("x", 32))
			args = append(args, "--url", server.URL)
		}
		var output bytes.Buffer
		old := (*main.OsStdout)
		(*main.OsStdout) = &output
		status := main.CmdDataRepair(args)
		(*main.OsStdout) = old
		server.Close()
		wantGets := 0
		if hosted {
			wantGets = 1
		}
		if status != 0 || posts != 1 || gets != wantGets || !json.Valid(output.Bytes()) || !strings.Contains(output.String(), parent) {
			t.Fatalf("replacement CLI hosted=%v status=%d GET=%d POST=%d output=%s", hosted, status, gets, posts, output.String())
		}
	}
}

func TestDataRepairReplacementCLIRequiresConfirmationAndNewID(t *testing.T) {
	for _, flags := range [][]string{
		{"--digest", strings.Repeat("a", 64), "--repair-operation", uuid.NewString()},
		{"--digest", strings.Repeat("a", 64), "--yes"},
		{"--repair-operation", uuid.NewString(), "--yes"},
	} {
		if status := main.CmdDataRepair(append([]string{"replace", uuid.NewString()}, flags...)); status == 0 {
			t.Fatal("unconfirmed or unidentifiable replacement accepted")
		}
	}
}
