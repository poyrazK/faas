package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/datarepair"
)

func TestDataRepairValidationCLIIsReadOnlyAndReturnsDriftJSON(t *testing.T) {
	for _, hosted := range []bool{false, true} {
		for _, drift := range []bool{false, true} {
			id, digest := uuid.NewString(), strings.Repeat("a", 64)
			r := datarepair.ValidationReport{PlanID: id, PlanDigest: digest,
				Scope:     datarepair.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"},
				CheckedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Minute).UTC(), Matches: !drift,
				Candidates: 460, Excluded: 40, Checked: 460, Unchanged: 460, Issues: []string{}, Rows: []datarepair.ValidationRow{}}
			if drift {
				r.Unchanged, r.Changed, r.Issues = 459, 1, []string{"candidate_drift"}
				r.Rows = []datarepair.ValidationRow{{RowIdentity: uuid.NewString(), KeyJSON: `{"id": 41}`, ExpectedVersion: "2", ObservedVersion: "3", Reason: datarepair.ReasonVersionChanged}}
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls++
				base := "/v1/data-repair/plans/"
				if hosted {
					base = "/v1/apps/products/data-repair/operations/"
				}
				if req.Method != "POST" || req.URL.Path != base+id+"/validate" || req.Header.Get("Authorization") == "" {
					t.Error("validation performed an extra read/mutation or left its authenticated origin")
				}
				var body map[string]any
				if json.NewDecoder(req.Body).Decode(&body) != nil || len(body) != 1 || body["digest"] != digest {
					t.Error("validation supplied mutation fields or changed the digest")
				}
				_ = json.NewEncoder(w).Encode(r)
			}))
			args := []string{"validate", id, "--digest", digest}
			if hosted {
				t.Setenv("FAAS_API", server.URL)
				t.Setenv("FAAS_TOKEN", "validation-reader-token")
				args = append(args, "--app", "products")
			} else {
				t.Setenv("GREGALE_REPAIR_TOKEN", strings.Repeat("x", 32))
				args = append(args, "--url", server.URL)
			}
			var output bytes.Buffer
			old := osStdout
			osStdout = &output
			status := cmdDataRepair(args)
			osStdout = old
			server.Close()
			var got datarepair.ValidationReport
			if calls != 1 || (status != 0) != drift || json.Unmarshal(output.Bytes(), &got) != nil || !got.Valid() || got.PlanID != id || got.Matches == drift {
				t.Fatalf("validation CLI hosted=%t drift=%t status=%d calls=%d output=%s", hosted, drift, status, calls, output.String())
			}
		}
	}
}
