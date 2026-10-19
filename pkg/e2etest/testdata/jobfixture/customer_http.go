// adr: 521
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const httpFixtureCSV = "id,count\nalice,1\n"

type httpFixtureInput struct {
	Mode string `json:"mode"`
	// ADR-1193: a webhook-submitted Operation receives the provider event
	// envelope instead; it completes without a relay directive.
	Webhook json.RawMessage `json:"webhook,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// httpFixturePaths are the definitions' routes: the base export, one with
// ADR-1196 limits and one bound to an inbound webhook (ADR-1287 Stage 1).
var httpFixturePaths = map[string]bool{"/exports": true, "/exports/bounded": true, "/exports/webhook": true}

type httpFixtureServer struct {
	client  func(context.Context) (*api.Client, error)
	crash   func(int)
	version string
}

func serveCustomerHTTP() error {
	version := os.Getenv("FIXTURE_VERSION")
	if version == "" {
		version = "original"
	}
	fixture := httpFixtureServer{client: nativeWorkflowFixtureClient, crash: os.Exit, version: version}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	server := &http.Server{Addr: ":" + port, Handler: fixture, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 4 * time.Minute}
	return server.ListenAndServe()
}

func (f httpFixtureServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
		_, _ = w.Write([]byte(`{"ok":true}`))
		return
	}
	if r.Method != http.MethodPost || !httpFixturePaths[r.URL.Path] {
		http.NotFound(w, r)
		return
	}
	id, proof, err := httpFixtureProof(r)
	if err != nil {
		http.Error(w, "trusted operation claim required", http.StatusBadRequest)
		return
	}
	var input httpFixtureInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		http.Error(w, "invalid fixture input", http.StatusBadRequest)
		return
	}
	if input.Webhook != nil {
		input.Mode = "complete"
	}
	output, err := f.run(r.Context(), id, proof, input)
	if err != nil {
		http.Error(w, "fixture handler stopped", http.StatusConflict)
		return
	}
	_ = json.NewEncoder(w).Encode(output)
}

// The synthetic adapter restores only the ordinary HTTP claim. Native
// workflow or Job context would mean the wrong adapter delivered the request.
func httpFixtureProof(r *http.Request) (string, api.OperationRuntimeProof, error) {
	id := r.Header.Get(api.OperationIDHeader)
	p := api.OperationRuntimeProof{InvocationID: r.Header.Get(api.InvocationIDHeader), Capability: r.Header.Get(api.OperationCapabilityHeader)}
	p.Attempt, _ = strconv.Atoi(r.Header.Get(api.OperationAttemptHeader))
	for _, value := range []string{id, p.InvocationID} {
		if _, err := uuid.Parse(value); err != nil {
			return "", p, fmt.Errorf("invalid operation identity")
		}
	}
	if capability, err := hex.DecodeString(p.Capability); err != nil || len(capability) != 32 || p.Attempt < 1 {
		return "", p, fmt.Errorf("invalid operation claim")
	}
	for _, native := range []string{api.OperationExecutionKindHeader, api.OperationWorkflowRunHeader, api.OperationWorkflowCapabilityHeader, api.OperationGenerationHeader} {
		if r.Header.Get(native) != "" {
			return "", p, fmt.Errorf("native execution context on an HTTP claim")
		}
	}
	return id, p, nil
}

func (f httpFixtureServer) control(ctx context.Context, id string, proof api.OperationRuntimeProof) (api.OperationExecutionControlResponse, error) {
	client, err := f.client(ctx)
	if err != nil {
		return api.OperationExecutionControlResponse{}, err
	}
	control, err := client.GetOperationExecutionControl(ctx, id, proof)
	if err == nil && control.CancellationRequested {
		err = errFixtureCancelled
	}
	return control, err
}

// The scheduler renews the HTTP lease while the handler runs, so only the
// admitted deadline bounds local work; control polls observe lost authority.
func (f httpFixtureServer) run(ctx context.Context, id string, proof api.OperationRuntimeProof, input httpFixtureInput) (any, error) {
	control, err := f.readyControl(ctx, id, proof)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, control.DeadlineAt.Sub(control.ObservedAt))
	defer cancel()
	if _, err := f.fixtureEvent(ctx, id, proof, "entered"); err != nil {
		return nil, err
	}
	if err := f.progress(ctx, id, proof, "generating"); err != nil {
		return nil, err
	}
	artifact, err := f.upload(ctx, id, proof)
	if err != nil {
		return nil, err
	}
	if err := f.progress(ctx, id, proof, "uploading"); err != nil {
		return nil, err
	}
	if input.Mode == "fanout" {
		// ADR-20261018183000: start one child with this attempt's proof, then
		// start it again to prove the key replays the same child.
		if err := f.fanout(ctx, id, proof); err != nil {
			return nil, err
		}
	} else if input.Mode != "complete" {
		status, err := f.waitDirective(ctx, id, proof)
		if err != nil {
			return nil, err
		}
		if status == http.StatusResetContent {
			f.crash(73) // Real process death after private retention, before HTTP reply.
			return nil, errFixtureUncertain
		}
	}
	if _, err := f.control(ctx, id, proof); err != nil {
		return nil, err
	}
	return map[string]any{"rows": 1, "artifact_id": artifact.ID, "version": f.version}, nil
}

// The jail socket can appear just before its acceptance relay binds. Retry
// transport readiness before business entry; denied claims stop at once.
func (f httpFixtureServer) readyControl(ctx context.Context, id string, proof api.OperationRuntimeProof) (api.OperationExecutionControlResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		control, err := f.control(ctx, id, proof)
		if err == nil {
			return control, nil
		}
		var problem *api.APIError
		if errors.As(err, &problem) && problem.Problem.Status < 500 || errors.Is(err, errFixtureCancelled) {
			return control, err
		}
		select {
		case <-ctx.Done():
			return control, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Reports for one attempt are spaced by ReportMinIntervalMS; a stable report
// ID makes the bounded retry after 429 idempotent.
func (f httpFixtureServer) progress(ctx context.Context, id string, proof api.OperationRuntimeProof, stage string) error {
	for retry := 0; ; retry++ {
		client, err := f.client(ctx)
		if err != nil {
			return err
		}
		_, err = client.ReportOperationProgress(ctx, id, proof, api.OperationReportRequest{ReportID: stage, Stage: stage, Completed: 1, Total: 1})
		var problem *api.APIError
		if err == nil || retry == 2 || !errors.As(err, &problem) || problem.Problem.Status != http.StatusTooManyRequests {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (f httpFixtureServer) upload(ctx context.Context, id string, proof api.OperationRuntimeProof) (*api.OperationResultArtifact, error) {
	declaration := api.OperationArtifactUploadRequest{ReportID: "export-csv", Name: "export.csv", SizeBytes: int64(len(httpFixtureCSV)), SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(httpFixtureCSV)))}
	client, err := f.client(ctx)
	if err != nil {
		return nil, err
	}
	receipt, err := client.ReuseOperationUpload(ctx, id, proof, declaration)
	if err != nil {
		return nil, err
	}
	if !receipt.Available {
		receipt, err = client.UploadOperationArtifact(ctx, id, proof, declaration, strings.NewReader(httpFixtureCSV))
		if err != nil {
			// A lost acknowledgement is resolved by receipt lookup, never by
			// transferring the bytes again.
			client, err = f.client(ctx)
			if err == nil {
				receipt, err = client.ReuseOperationUpload(ctx, id, proof, declaration)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if !receipt.Available || receipt.Artifact == nil {
		return nil, fmt.Errorf("verified operation receipt unavailable")
	}
	return receipt.Artifact, nil
}

// waitDirective holds the handler until the acceptance relay releases it
// (204) or requests process death (205), observing cancellation meanwhile.
func (f httpFixtureServer) waitDirective(ctx context.Context, id string, proof api.OperationRuntimeProof) (int, error) {
	for {
		if _, err := f.control(ctx, id, proof); err != nil {
			return 0, err
		}
		status, err := f.fixtureEvent(ctx, id, proof, "release")
		if err != nil {
			return 0, err
		}
		if status != http.StatusAccepted {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (f httpFixtureServer) fixtureEvent(ctx context.Context, id string, proof api.OperationRuntimeProof, action string) (int, error) {
	client, err := f.client(ctx)
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.BaseURL()+"/fixture/"+action+"/"+id+"/"+proof.InvocationID, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+client.Token())
	response, err := client.HTTPClient().Do(request)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	switch response.StatusCode {
	case http.StatusAccepted, http.StatusNoContent, http.StatusResetContent:
		return response.StatusCode, nil
	}
	return response.StatusCode, fmt.Errorf("fixture barrier unavailable")
}

// httpFixtureChildKey names the one child a fanout parent starts.
const httpFixtureChildKey = "child-1"

// fanout starts the child twice: the first start creates it, the replay must
// return the same child. Child routes are gated and outside the SDK, so the
// fixture sends the runtime proof headers itself.
func (f httpFixtureServer) fanout(ctx context.Context, id string, proof api.OperationRuntimeProof) error {
	first, err := f.startChild(ctx, id, proof, http.StatusCreated)
	if err != nil {
		return err
	}
	replay, err := f.startChild(ctx, id, proof, http.StatusOK)
	if err != nil {
		return err
	}
	if !replay.Replayed || replay.Operation.ID != first.Operation.ID || first.Key != httpFixtureChildKey {
		return fmt.Errorf("child start did not replay by key")
	}
	return nil
}

func (f httpFixtureServer) startChild(ctx context.Context, id string, proof api.OperationRuntimeProof, want int) (api.OperationChildStartResponse, error) {
	var out api.OperationChildStartResponse
	client, err := f.client(ctx)
	if err != nil {
		return out, err
	}
	body, _ := json.Marshal(api.OperationChildRequest{Operation: "export", Key: httpFixtureChildKey, Input: json.RawMessage(`{"mode":"complete"}`)})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.BaseURL()+"/v1/runtime/operations/"+id+"/children", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	request.Header.Set("Authorization", "Bearer "+client.Token())
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(api.InvocationIDHeader, proof.InvocationID)
	request.Header.Set(api.OperationAttemptHeader, strconv.Itoa(proof.Attempt))
	request.Header.Set(api.OperationCapabilityHeader, proof.Capability)
	response, err := client.HTTPClient().Do(request)
	if err != nil {
		return out, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != want {
		return out, fmt.Errorf("child start answered %d, want %d", response.StatusCode, want)
	}
	return out, json.NewDecoder(response.Body).Decode(&out)
}
