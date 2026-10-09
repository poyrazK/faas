// adr: 671
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const workflowFixtureCSV = "id,count\nalice,1\n"

type workflowFixtureInput struct {
	Mode string `json:"mode"`
	Rows int    `json:"rows,omitempty"`
}

type workflowFixtureServer struct {
	client  func(context.Context) (*api.Client, error)
	crash   func(int)
	version string
}

func serveCustomerWorkflow() error {
	version := os.Getenv("FIXTURE_VERSION")
	if version == "" {
		version = "original"
	}
	fixture := workflowFixtureServer{client: nativeWorkflowFixtureClient, crash: os.Exit, version: version}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	server := &http.Server{Addr: ":" + port, Handler: fixture, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 4 * time.Minute}
	return server.ListenAndServe()
}

// Minting stays in vmmd; neither the guest fixture nor its relay has a key.
func nativeWorkflowFixtureClient(ctx context.Context) (*api.Client, error) {
	endpoint, err := url.Parse(os.Getenv("FAAS_WORKLOAD_IDENTITY_ENDPOINT"))
	if err != nil || endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" || endpoint.User != nil {
		return nil, fmt.Errorf("missing trusted loopback workload identity endpoint")
	}
	query := endpoint.Query()
	query.Set("audience", "gregale:operations")
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	identity := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := identity.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&token) != nil || token.AccessToken == "" {
		return nil, fmt.Errorf("workload identity unavailable")
	}
	client := api.NewClient("http://localhost", token.AccessToken)
	client.HTTPClient().Transport = &http.Transport{DialContext: dialCustomerFixtureVSock, DisableKeepAlives: true}
	client.HTTPClient().Timeout = 5 * time.Second
	return client, nil
}

func (f workflowFixtureServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
		_, _ = w.Write([]byte(`{"ok":true}`))
		return
	}
	if r.Method != http.MethodPost || (r.URL.Path != "/collect" && r.URL.Path != "/transform" && r.URL.Path != "/finish") {
		http.NotFound(w, r)
		return
	}
	id, proof, err := workflowFixtureProof(r)
	if err != nil {
		http.Error(w, "trusted workflow proof required", http.StatusBadRequest)
		return
	}
	var input workflowFixtureInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		http.Error(w, "invalid fixture input", http.StatusBadRequest)
		return
	}
	output, err := f.run(r.Context(), id, proof, input)
	if err != nil {
		http.Error(w, "fixture action stopped", http.StatusConflict)
		return
	}
	_ = json.NewEncoder(w).Encode(output)
}

func workflowFixtureProof(r *http.Request) (string, api.OperationWorkflowRuntimeProof, error) {
	id := r.Header.Get(api.OperationIDHeader)
	p := api.OperationWorkflowRuntimeProof{RunID: r.Header.Get(api.OperationWorkflowRunHeader), StepName: r.Header.Get(api.OperationWorkflowStepHeader), Capability: r.Header.Get(api.OperationWorkflowCapabilityHeader)}
	p.Generation, _ = strconv.Atoi(r.Header.Get(api.OperationGenerationHeader))
	p.Attempt, _ = strconv.Atoi(r.Header.Get(api.OperationAttemptHeader))
	for _, value := range []string{id, p.RunID, p.Capability} {
		if _, err := uuid.Parse(value); err != nil {
			return "", p, fmt.Errorf("invalid native identity")
		}
	}
	if r.Header.Get(api.OperationExecutionKindHeader) != "workflow" || r.Header.Get(api.InvocationIDHeader) != "" || p.StepName != strings.TrimPrefix(r.URL.Path, "/") || p.Generation < 1 || p.Attempt < 1 {
		return "", p, fmt.Errorf("invalid native proof")
	}
	return id, p, nil
}

func (f workflowFixtureServer) control(ctx context.Context, id string, proof api.OperationWorkflowRuntimeProof) (api.OperationWorkflowControlResponse, error) {
	client, err := f.client(ctx)
	if err != nil {
		return api.OperationWorkflowControlResponse{}, err
	}
	control, err := client.GetWorkflowOperationExecutionControl(ctx, id, proof)
	if err == nil && control.CancellationRequested {
		err = errFixtureCancelled
	}
	return control, err
}

func (f workflowFixtureServer) run(ctx context.Context, id string, proof api.OperationWorkflowRuntimeProof, input workflowFixtureInput) (any, error) {
	control, err := f.readyControl(ctx, id, proof)
	if err != nil {
		return nil, err
	}
	bound := control.DeadlineAt
	if control.LeaseExpiresAt.Before(bound) {
		bound = control.LeaseExpiresAt
	}
	ctx, cancel := context.WithTimeout(ctx, bound.Sub(control.ObservedAt))
	defer cancel()
	if _, err := f.fixtureEvent(ctx, id, proof, "entered"); err != nil {
		return nil, err
	}
	if proof.StepName != "finish" {
		return workflowFixtureInput{Mode: input.Mode, Rows: 1}, nil
	}
	artifact, err := f.upload(ctx, id, proof)
	if err != nil {
		return nil, err
	}
	if input.Mode != "complete" && (input.Mode != "crash" || proof.Generation <= 1) {
		if err := f.waitRelease(ctx, id, proof); err != nil {
			return nil, err
		}
	}
	if input.Mode == "crash" && proof.Generation == 1 {
		f.crash(73) // Real process death after private retention, before HTTP reply.
		return nil, errFixtureUncertain
	}
	if _, err := f.control(ctx, id, proof); err != nil {
		return nil, err
	}
	return map[string]any{"rows": 1, "artifact_id": artifact.ID, "version": f.version}, nil
}

// The jail socket can appear just before its acceptance relay binds. Retry
// transport readiness before business entry; denied native proofs stop at once.
func (f workflowFixtureServer) readyControl(ctx context.Context, id string, proof api.OperationWorkflowRuntimeProof) (api.OperationWorkflowControlResponse, error) {
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

func (f workflowFixtureServer) upload(ctx context.Context, id string, proof api.OperationWorkflowRuntimeProof) (*api.OperationResultArtifact, error) {
	declaration := api.OperationArtifactUploadRequest{ReportID: "export-csv", Name: "export.csv", SizeBytes: int64(len(workflowFixtureCSV)), SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(workflowFixtureCSV)))}
	client, err := f.client(ctx)
	if err != nil {
		return nil, err
	}
	receipt, err := client.ReuseWorkflowOperationUpload(ctx, id, proof, declaration)
	if err != nil {
		return nil, err
	}
	if !receipt.Available {
		receipt, err = client.UploadWorkflowOperationArtifact(ctx, id, proof, declaration, strings.NewReader(workflowFixtureCSV))
		if err != nil {
			client, err = f.client(ctx)
			if err == nil {
				receipt, err = client.ReuseWorkflowOperationUpload(ctx, id, proof, declaration)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if !receipt.Available || receipt.Artifact == nil {
		return nil, fmt.Errorf("verified workflow receipt unavailable")
	}
	return receipt.Artifact, nil
}

func (f workflowFixtureServer) waitRelease(ctx context.Context, id string, proof api.OperationWorkflowRuntimeProof) error {
	for {
		if _, err := f.control(ctx, id, proof); err != nil {
			return err
		}
		status, err := f.fixtureEvent(ctx, id, proof, "release")
		if err != nil {
			return err
		}
		if status == http.StatusNoContent {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (f workflowFixtureServer) fixtureEvent(ctx context.Context, id string, proof api.OperationWorkflowRuntimeProof, action string) (int, error) {
	client, err := f.client(ctx)
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.BaseURL()+"/fixture/"+action+"/"+id+"/"+proof.StepName, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+client.Token())
	request.Header.Set(api.OperationGenerationHeader, strconv.Itoa(proof.Generation))
	request.Header.Set(api.OperationAttemptHeader, strconv.Itoa(proof.Attempt))
	response, err := client.HTTPClient().Do(request)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusNoContent {
		return response.StatusCode, fmt.Errorf("fixture barrier unavailable")
	}
	return response.StatusCode, nil
}
