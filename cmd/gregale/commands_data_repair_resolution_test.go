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

func TestDataRepairResolveCLIRequiresSourceProof(t *testing.T) {
	for _, hosted := range []bool{false, true} {
		for _, outcome := range []string{"closed", "committed", "unproven"} {
			id, repairID, digest := uuid.NewString(), uuid.NewString(), strings.Repeat("a", 64)
			if hosted {
				repairID = id
			}
			actor, at := "reviewer", time.Now().UTC()
			p := datarepair.Plan{ID: id, SourceOperationID: uuid.NewString(), Digest: digest,
				Scope: datarepair.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}, ApprovedBy: &actor}
			switch outcome {
			case "closed":
				p.ApplyResolution = &datarepair.ApplyResolution{Outcome: datarepair.ResolutionNotCommitted, PlanDigest: digest, RepairOperationID: repairID, ResolvedAt: at, ResolvedBy: actor}
			case "committed":
				p.AppliedAt, p.RepairOperationID = &at, &repairID
			}
			posts, gets := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				base := "/v1/data-repair/plans/"
				if hosted {
					base = "/v1/apps/products/data-repair/operations/"
				}
				if r.Header.Get("Authorization") == "" {
					t.Error("resolution omitted authentication")
				}
				if hosted && r.Method == "GET" && r.URL.Path == base+id {
					gets++
					_ = json.NewEncoder(w).Encode(repairapi.ManagedResponse{Operation: api.OperationResponse{ID: id, LatestSequence: 5}, Plan: datarepair.Plan{ID: id, Digest: digest}})
					return
				}
				posts++
				if r.Method != "POST" || r.URL.Path != base+id+"/resolve" {
					t.Error("resolution left its locally constructed endpoint")
				}
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil || body["digest"] != digest || len(body) != 2 {
					t.Error("resolution changed digest or supplied extra authority")
				}
				if hosted {
					if body["expected_sequence"] != float64(5) || r.Header.Get("Idempotency-Key") != "" {
						t.Error("resolution changed hosted operation identity/CAS sequence")
					}
					_ = json.NewEncoder(w).Encode(repairapi.ManagedResponse{Operation: api.OperationResponse{ID: id}, Plan: p})
				} else {
					if body["repair_operation_id"] != repairID {
						t.Error("resolution lost the original apply identity")
					}
					_ = json.NewEncoder(w).Encode(p)
				}
			}))
			args := []string{"resolve", id, "--digest", digest, "--repair-operation", repairID, "--yes"}
			if hosted {
				t.Setenv("FAAS_API", server.URL)
				t.Setenv("FAAS_TOKEN", "resolution-manager-token")
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
			if (status == 0) != (outcome != "unproven") || posts != 1 || gets != wantGets || (status == 0 && !json.Valid(output.Bytes())) {
				t.Fatalf("resolution CLI hosted=%v outcome=%s status=%d GET=%d POST=%d output=%s", hosted, outcome, status, gets, posts, output.String())
			}
		}
	}
}

func TestDataRepairResolveCLIRequiresExplicitClosureAndIdentity(t *testing.T) {
	for _, flags := range [][]string{
		{"--digest", strings.Repeat("a", 64), "--repair-operation", uuid.NewString()},
		{"--digest", strings.Repeat("a", 64), "--yes"},
		{"--repair-operation", uuid.NewString(), "--yes"},
	} {
		if main.CmdDataRepair(append([]string{"resolve", uuid.NewString()}, flags...)) == 0 {
			t.Fatal("unconfirmed or unidentifiable closure accepted")
		}
	}
}
