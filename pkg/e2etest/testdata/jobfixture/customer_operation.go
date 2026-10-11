package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var errFixtureCancelled = errors.New("customer operation cancellation observed")
var errFixtureUncertain = errors.New("injected failure after private result preparation")

type customerOperationInput struct {
	Mode         string                        `json:"mode"`
	Artifact     *api.OperationArtifactRequest `json:"artifact,omitempty"`
	ArtifactData string                        `json:"artifact_data,omitempty"`
}

func customerOperationFromEnv() error {
	proof := api.OperationJobRuntimeProof{RunID: os.Getenv("GREGALE_RUN_ID"), InstanceID: os.Getenv("GREGALE_CUSTOMER_OPERATION_JOB_INSTANCE_ID"), Capability: os.Getenv("GREGALE_CUSTOMER_OPERATION_JOB_CAPABILITY")}
	var err error
	proof.Generation, err = strconv.Atoi(os.Getenv("GREGALE_CUSTOMER_OPERATION_GENERATION"))
	if err != nil {
		return fmt.Errorf("missing generation")
	}
	proof.Attempt, err = strconv.Atoi(os.Getenv("GREGALE_TASK_ATTEMPT"))
	if err != nil {
		return fmt.Errorf("missing native attempt")
	}
	id := os.Getenv("GREGALE_CUSTOMER_OPERATION_ID")
	if id == "" || proof.RunID == "" || proof.InstanceID == "" || proof.Capability == "" {
		return fmt.Errorf("missing native operation proof")
	}
	var input customerOperationInput
	if err := json.Unmarshal([]byte(os.Getenv("GREGALE_CUSTOMER_OPERATION_INPUT")), &input); err != nil {
		return fmt.Errorf("invalid operation input")
	}
	client := api.NewClient("http://localhost", "")
	client.HTTPClient().Transport = &http.Transport{DialContext: dialCustomerFixtureVSock, DisableKeepAlives: true}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	// The host test binds its per-instance test relay as the jail is created.
	for {
		if _, err := client.GetJobOperationExecutionControl(ctx, id, proof); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return runCustomerOperation(ctx, client, id, proof, input)
}

func runCustomerOperation(ctx context.Context, client *api.Client, id string, proof api.OperationJobRuntimeProof, input customerOperationInput) error {
	control, err := client.GetJobOperationExecutionControl(ctx, id, proof)
	if err != nil {
		return err
	}
	if control.CancellationRequested {
		return errFixtureCancelled
	}
	if _, err := client.ReportJobOperationProgress(ctx, id, proof, api.OperationJobReportRequest{ReportID: "started", Progress: &api.OperationReportRequest{Stage: "generating", Completed: 0, Total: 1}}); err != nil {
		return err
	}
	if input.Mode == "cancel" {
		return waitCustomerOperationRelease(ctx, client, id, proof, false)
	}
	if input.Artifact != nil {
		var receipt api.OperationJobArtifactResponse
		var err error
		if input.Mode == "direct-hold" {
			a := input.Artifact
			declaration := api.OperationArtifactUploadRequest{ReportID: a.ReportID, Name: a.Name, SizeBytes: a.SizeBytes, SHA256: a.SHA256}
			receipt, err = client.UploadJobOperationArtifact(ctx, id, proof, declaration, strings.NewReader(input.ArtifactData))
			if err != nil {
				receipt, err = client.ReuseJobOperationUpload(ctx, id, proof, declaration)
			}
		} else {
			receipt, err = client.PrepareJobOperationArtifact(ctx, id, proof, *input.Artifact)
			if err != nil {
				receipt, err = client.ReuseJobOperationArtifact(ctx, id, proof, *input.Artifact)
			}
		}
		if err != nil {
			return err
		}
		if !receipt.Available || receipt.Artifact == nil {
			return fmt.Errorf("prepared file acknowledgement unavailable")
		}
	}
	if _, err := client.PrepareJobOperationResult(ctx, id, proof, api.OperationJobReportRequest{ReportID: "finished", Result: json.RawMessage(`{"file":"export.csv"}`)}); err != nil {
		return err
	}
	if input.Mode == "hold" || input.Mode == "direct-hold" {
		return waitCustomerOperationRelease(ctx, client, id, proof, true)
	}
	if input.Mode == "uncertain" && proof.Generation == 1 {
		return errFixtureUncertain
	}
	return nil
}

func waitCustomerOperationRelease(ctx context.Context, client *api.Client, id string, proof api.OperationJobRuntimeProof, allowRelease bool) error {
	for {
		control, err := client.GetJobOperationExecutionControl(ctx, id, proof)
		if err == nil && control.CancellationRequested {
			return errFixtureCancelled
		}
		if err == nil && allowRelease {
			r, err := http.NewRequestWithContext(ctx, http.MethodGet, client.BaseURL()+"/fixture/release", nil)
			if err != nil {
				return err
			}
			response, err := client.HTTPClient().Do(r)
			if err == nil {
				released := response.StatusCode == http.StatusNoContent
				_ = response.Body.Close()
				if released {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
