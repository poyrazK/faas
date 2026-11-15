package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestReviewedCronMutationHTTP(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	cronID, appID := seedCron(t, e, "reviewed-cron", "0 3 * * *")
	read := e.Do(t, http.MethodGet, "/v1/crons/"+cronID, nil, nil)
	var cron api.CronResponse
	if read.Code != 200 || json.Unmarshal(read.Body.Bytes(), &cron) != nil || !api.ValidEnvBatchReceiptProof(cron.ReviewToken) {
		t.Fatalf("review: %d %s", read.Code, read.Body)
	}
	path := "/updated"
	r := api.CronMutationRequest{RequestID: uuid.NewString(), AppID: appID, CronID: cronID, ExpectedState: cron.ReviewToken, ReceiptProof: strings.Repeat("a", 64), ExpiresAt: time.Now().UTC().Add(time.Minute), Path: &path}
	endpoint := "/v1/apps/reviewed-cron/cron-mutations"
	first := e.Do(t, http.MethodPost, endpoint, r, nil)
	var receipt api.CronMutationReceipt
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &receipt) != nil || receipt.Status != "completed" {
		t.Fatalf("apply: %d %s", first.Code, first.Body)
	}
	if strings.Contains(first.Body.String(), r.ReceiptProof) || strings.Contains(first.Body.String(), path) {
		t.Fatal("receipt exposed private inputs")
	}
	for _, proof := range []string{"", strings.Repeat("b", 64), r.ReceiptProof} {
		headers := map[string]string{}
		if proof != "" {
			headers[api.CronMutationReceiptProofHeader] = proof
		}
		out := e.Do(t, http.MethodGet, endpoint+"/"+r.RequestID, nil, headers)
		want := 404
		if proof == "" {
			want = 400
		}
		if proof == r.ReceiptProof {
			want = 200
		}
		if out.Code != want {
			t.Fatalf("receipt proof: %d %s", out.Code, out.Body)
		}
	}
	// Reusing an attempt with changed inputs is never another write.
	other := "/other"
	r.Path = &other
	if out := e.Do(t, http.MethodPost, endpoint, r, nil); out.Code != 409 {
		t.Fatalf("changed retry: %d %s", out.Code, out.Body)
	}
	r.RequestID = uuid.NewString()
	if out := e.Do(t, http.MethodPost, endpoint, r, nil); out.Code != 409 {
		t.Fatalf("stale review: %d %s", out.Code, out.Body)
	}
	// A destructive body cannot bypass its separately classified route.
	r.Path = nil
	r.Delete = true
	if out := e.Do(t, http.MethodPost, endpoint, r, nil); out.Code != 400 {
		t.Fatalf("deletion on update route: %d %s", out.Code, out.Body)
	}
}

func TestReviewedCronDeletionHTTP(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	cronID, appID := seedCron(t, e, "reviewed-delete", "0 3 * * *")
	var cron api.CronResponse
	_ = json.Unmarshal(e.Do(t, http.MethodGet, "/v1/crons/"+cronID, nil, nil).Body.Bytes(), &cron)
	r := api.CronMutationRequest{RequestID: uuid.NewString(), AppID: appID, CronID: cronID, ExpectedState: cron.ReviewToken, ReceiptProof: strings.Repeat("c", 64), ExpiresAt: time.Now().UTC().Add(time.Minute), Delete: true}
	var receipt api.CronMutationReceipt
	for i := 0; i < 8; i++ {
		out := e.Do(t, http.MethodPost, "/v1/apps/reviewed-delete/cron-deletions", r, nil)
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &receipt) != nil {
			t.Fatalf("delete: %d %s", out.Code, out.Body)
		}
		if receipt.Status == "completed" {
			break
		}
	}
	if receipt.Status != "completed" {
		t.Fatalf("deletion unfinished: %+v", receipt)
	}
	if out := e.Do(t, http.MethodGet, "/v1/crons/"+cronID, nil, nil); out.Code != 404 {
		t.Fatalf("deleted cron: %d", out.Code)
	}
	if out := e.Do(t, http.MethodGet, "/v1/apps/reviewed-delete/cron-mutations/"+r.RequestID, nil, map[string]string{api.CronMutationReceiptProofHeader: r.ReceiptProof}); out.Code != 200 {
		t.Fatalf("receipt after deletion: %d %s", out.Code, out.Body)
	}
}
