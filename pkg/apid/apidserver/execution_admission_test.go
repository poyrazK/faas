package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestSourceExecutionAtomicReplayBeforeQuotaAndPreparation(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	apidserver.EnableExecutionAPIForTest(t, &e)
	request := apidserver.ExecutionRequest()
	headers := map[string]string{"Idempotency-Key": "source-retry"}
	first := e.Do(t, http.MethodPost, "/v1/executions", request, headers)
	if first.Code != http.StatusAccepted {
		t.Fatalf("create: %d %s", first.Code, first.Body.String())
	}
	var receipt api.ExecutionResponse
	if err := json.Unmarshal(first.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	// Quota is full; the encryption recipient also disappears. Neither can
	// prevent recovery of an already admitted receipt.
	previous := *apidserver.SetSecretRecipient
	*apidserver.SetSecretRecipient = nil
	t.Cleanup(func() { *apidserver.SetSecretRecipient = previous })
	for range 2 {
		retry := e.Do(t, http.MethodPost, "/v1/executions", request, headers)
		if retry.Code != http.StatusAccepted || retry.Header().Get("Idempotent-Replayed") != "true" {
			t.Fatalf("retry: %d %s", retry.Code, retry.Body.String())
		}
		var got api.ExecutionResponse
		if err := json.Unmarshal(retry.Body.Bytes(), &got); err != nil || got.ID != receipt.ID || got.CreatedAt != receipt.CreatedAt {
			t.Fatalf("receipt changed: %+v, %v", got, err)
		}
	}
	request.Source += "\n// different request"
	conflict := e.Do(t, http.MethodPost, "/v1/executions", request, headers)
	if conflict.Code != http.StatusUnprocessableEntity || !strings.Contains(conflict.Body.String(), api.CodeIdempotencyKeyReused) {
		t.Fatalf("conflict: %d %s", conflict.Code, conflict.Body.String())
	}
}

func TestSourceExecutionLargeRequestReplayAndSigningKeyFailure(t *testing.T) {
	e := apidserver.Setup(t, api.PlanScale)
	apidserver.EnableExecutionAPIForTest(t, &e)
	request := apidserver.ExecutionRequest()
	request.Source = strings.Repeat(" ", 700<<10) + request.Source
	value := strings.Repeat("x", 400<<10)
	request.Input = json.RawMessage(`{"b":2,"a":"` + value + `"}`)
	headers := map[string]string{"Idempotency-Key": "large-request"}
	first := e.Do(t, http.MethodPost, "/v1/executions", request, headers)
	if first.Code != http.StatusAccepted {
		t.Fatalf("large admission: %d %s", first.Code, first.Body.String())
	}
	var receipt api.ExecutionResponse
	if err := json.Unmarshal(first.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	request.Input = json.RawMessage(`{ "a":"` + value + `", "b": 2 }`)
	originalKey := *apidserver.HostHMACKey
	t.Cleanup(func() { *apidserver.HostHMACKey = originalKey })
	for _, key := range []func() []byte{nil, func() []byte { return []byte("different-host-key") }} {
		*apidserver.HostHMACKey = key
		retry := e.Do(t, http.MethodPost, "/v1/executions", request, headers)
		if retry.Code != http.StatusServiceUnavailable {
			t.Fatalf("unverifiable replay: %d %s", retry.Code, retry.Body.String())
		}
	}
	*apidserver.HostHMACKey = originalKey
	retry := e.Do(t, http.MethodPost, "/v1/executions", request, headers)
	var recovered api.ExecutionResponse
	if err := json.Unmarshal(retry.Body.Bytes(), &recovered); err != nil || retry.Code != http.StatusAccepted || recovered.ID != receipt.ID || retry.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("large canonical replay: %d %s, %v", retry.Code, retry.Body.String(), err)
	}
	request.Input = json.RawMessage(`{"b":3}`)
	changed := e.Do(t, http.MethodPost, "/v1/executions", request, headers)
	if changed.Code != http.StatusUnprocessableEntity {
		t.Fatalf("large changed request: %d %s", changed.Code, changed.Body.String())
	}
	rows, err := (*e.Store()).ListExecutions(context.Background(), (*e.Acct()).ID, 100, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("large retries created %d Runs: %v", len(rows), err)
	}
}

func TestSourceExecutionConcurrentGrantRetriesAdmitOnce(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.EnableExecutionAPIForTest(t, &e)
	producer := apidserver.CreateRunsOnlyKey(t, e, "atomic-producer")
	consumer := apidserver.CreateRunsOnlyKey(t, e, "atomic-consumer")
	source := apidserver.CreateExecutionAs(t, e, producer, "atomic-source", apidserver.ExecutionRequest())
	apidserver.FinishExecutionWithArtifact(t, e, source.ID)
	grant := createExecutionArtifactGrantAs(t, e, producer, source.ID, "result.csv")
	const callers = 8
	start := make(chan struct{})
	type result struct {
		code         int
		body, replay string
	}
	results := make(chan result, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			<-start
			r := createExecutionUsingArtifactGrantAs(t, e, consumer, "atomic-consume", grant.Token)
			results <- result{r.Code, r.Body.String(), r.Header().Get("Idempotent-Replayed")}
		})
	}
	close(start)
	wg.Wait()
	close(results)
	id, first := "", 0
	for result := range results {
		if result.code != http.StatusAccepted {
			t.Fatalf("concurrent retry: %d %s", result.code, result.body)
		}
		var receipt api.ExecutionResponse
		if err := json.Unmarshal([]byte(result.body), &receipt); err != nil {
			t.Fatal(err)
		}
		if id == "" {
			id = receipt.ID
		}
		if receipt.ID != id {
			t.Fatalf("duplicate receipts: %s, %s", id, receipt.ID)
		}
		if result.replay == "" {
			first++
		}
	}
	if first != 1 {
		t.Fatalf("first admissions=%d", first)
	}
	// A different logical operation cannot consume the same capability.
	denied := createExecutionUsingArtifactGrantAs(t, e, consumer, "different-operation", grant.Token)
	if denied.Code != http.StatusNotFound {
		t.Fatalf("grant redeemed twice: %d %s", denied.Code, denied.Body.String())
	}
}

type executionAdmissionFaultStore struct {
	MemStore    *state.MemStore
	afterCommit bool
	fail        bool
}

func (s *executionAdmissionFaultStore) AdmitExecution(ctx context.Context, identity state.ExecutionAdmissionIdentity, prepare state.PrepareExecutionAdmission) (state.Execution, bool, error) {
	if !s.afterCommit {
		return state.Execution{}, false, errors.New("admission database unavailable")
	}
	row, replayed, err := s.MemStore.AdmitExecution(ctx, identity, prepare)
	if err == nil && s.fail {
		s.fail = false
		return state.Execution{}, false, errors.New("lost commit acknowledgement")
	}
	return row, replayed, err
}

func TestSourceExecutionPersistenceErrorsFailClosedAndLostResponseRecovers(t *testing.T) {
	for _, afterCommit := range []bool{false, true} {
		t.Run(map[bool]string{false: "unavailable", true: "lost acknowledgement"}[afterCommit], func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			apidserver.EnableExecutionAPIForTest(t, &e)
			fake := &executionAdmissionFaultStore{MemStore: *e.Store(), afterCommit: afterCommit, fail: true}
			*(*e.S()).Store() = statetest.NewMem(fake)
			request := apidserver.ExecutionRequest()
			headers := map[string]string{"Idempotency-Key": "fault-recovery"}
			first := e.Do(t, http.MethodPost, "/v1/executions", request, headers)
			if first.Code < 500 {
				t.Fatalf("persistence error: %d %s", first.Code, first.Body.String())
			}
			rows, err := (*e.Store()).ListExecutions(context.Background(), (*e.Acct()).ID, 100, 0)
			want := 0
			if afterCommit {
				want = 1
			}
			if err != nil || len(rows) != want {
				t.Fatalf("Runs=%d, err=%v; want %d", len(rows), err, want)
			}
			if afterCommit {
				retry := e.Do(t, http.MethodPost, "/v1/executions", request, headers)
				var recovered api.ExecutionResponse
				if err := json.Unmarshal(retry.Body.Bytes(), &recovered); err != nil || retry.Code != http.StatusAccepted || recovered.ID != rows[0].ID || retry.Header().Get("Idempotent-Replayed") != "true" {
					t.Fatalf("recovery: %d %s, %v", retry.Code, retry.Body.String(), err)
				}
			}
		})
	}
}
