// adr: 521
// This process is driven by the PostgreSQL/HTTP acceptance test in cmd/apid.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

type configuration struct {
	API, Token, ForeignToken, DefinitionID, OperationID string
}

func main() {
	if err := inspect(); err != nil {
		// Diagnostics identify the failed contract, never credentials or data.
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Go Operations inspection passed")
}

func inspect() error {
	var config configuration
	if json.Unmarshal([]byte(os.Getenv("GREGALE_OPERATIONS_TEST_CONFIG")), &config) != nil {
		return errors.New("invalid Go Operations inspector configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := faas.NewClient(config.API, config.Token)
	if err != nil {
		return errors.New("go operations client construction failed")
	}
	for range 2 {
		receipt, err := client.StartPlatformTenantSelfOperation(ctx, faas.OperationStartRequest{DefinitionID: config.DefinitionID, Input: json.RawMessage(`{"count":1}`)}, "customer-export")
		if err != nil || receipt.ID != config.OperationID {
			return errors.New("go operations submission replay changed logical identity")
		}
	}
	_, err = client.StartPlatformTenantSelfOperation(ctx, faas.OperationStartRequest{DefinitionID: config.DefinitionID, Input: json.RawMessage(`{"count":2}`)}, "customer-export")
	var problem *faas.APIError
	if !errors.As(err, &problem) || problem.Problem.Status != 409 {
		return errors.New("go operations changed-input conflict was lost")
	}
	operation, err := client.GetPlatformTenantSelfOperation(ctx, config.OperationID)
	if err != nil || operation.State != faas.OperationSucceeded || operation.CompletionDelivery.LastError == "" || operation.Progress == nil || operation.Progress.Stage != "uploading" || len(operation.Artifacts) != 1 {
		return errors.New("go operations business/progress/delivery projection changed")
	}
	var result struct {
		OperationID string `json:"operation_id"`
		File        string `json:"file"`
	}
	if json.Unmarshal(operation.Result, &result) != nil || result.OperationID != config.OperationID || result.File != operation.Artifacts[0].ID {
		return errors.New("go operations typed result reference changed")
	}
	page, err := client.GetPlatformTenantSelfOperationEvents(ctx, config.OperationID, 2)
	if err != nil || page.ResyncRequired || len(page.Events) == 0 || page.LatestSequence != operation.LatestSequence {
		return errors.New("go operations durable event page changed")
	}
	for i, event := range page.Events {
		if event.OperationID != config.OperationID || event.Sequence != 3+int64(i) {
			return errors.New("go operations durable event ownership or order changed")
		}
	}
	var downloaded bytes.Buffer
	if _, err := client.DownloadPlatformTenantSelfOperationArtifact(ctx, config.OperationID, operation.Artifacts[0].ID, &downloaded); err != nil || downloaded.String() != "id,count\nalice,1\n" {
		return errors.New("go operations retained artifact verification failed")
	}
	client.SetToken(config.ForeignToken)
	_, err = client.GetPlatformTenantSelfOperation(ctx, config.OperationID)
	if !errors.As(err, &problem) || problem.Problem.Status != 404 {
		return errors.New("go operations foreign tenant read was not hidden")
	}
	client.SetToken(config.Token)
	body, err := client.StreamPlatformTenantSelfOperationEvents(ctx, config.OperationID, 2)
	if err != nil {
		return errors.New("go operations progress stream failed to open")
	}
	defer func() { _ = body.Close() }()
	decoder := faas.NewDecoder(body)
	defer func() { _ = decoder.Close() }()
	events, streamErrors := decoder.Events(), decoder.Errors()
	cursor := int64(2)
	for cursor < operation.LatestSequence {
		select {
		case <-ctx.Done():
			return errors.New("go operations progress stream timed out")
		case frame, ok := <-events:
			if !ok {
				return errors.New("go operations progress stream ended before replay")
			}
			switch frame.Event {
			case "snapshot", "resync":
				var snapshot faas.OperationResponse
				if json.Unmarshal([]byte(frame.Data), &snapshot) != nil || snapshot.ID != config.OperationID {
					return errors.New("go operations stream snapshot ownership changed")
				}
				if frame.Event == "resync" {
					cursor = snapshot.LatestSequence
				}
			case "operation":
				var event faas.OperationEvent
				if json.Unmarshal([]byte(frame.Data), &event) != nil || event.OperationID != config.OperationID || event.Sequence != cursor+1 || frame.ID != strconv.FormatInt(event.Sequence, 10) {
					return errors.New("go operations stream cursor changed")
				}
				cursor = event.Sequence
			case "auth_expired", "unavailable":
				return errors.New("go operations stream unexpectedly lost access")
			}
		case err, ok := <-streamErrors:
			if !ok {
				streamErrors = nil
			} else if err != nil {
				return errors.New("go operations progress stream failed to decode")
			}
		}
	}
	return nil
}
