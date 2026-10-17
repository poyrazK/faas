package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

type deliveryCommandInput struct {
	internalmail.DeliveryControlRequest
	wait time.Duration
}

func controlsCommand(key string) bool {
	switch key {
	case "delivery status", "delivery pause", "delivery resume", "delivery drain", "messages cancel", "recipients cancel":
		return true
	}
	return false
}
func controlCLIRoute(route string) bool {
	return strings.HasPrefix(route, internalmail.DeliveryControlsPath) || strings.HasSuffix(route, "/cancel")
}
func parseControlCommand(args []string, diagnostics io.Writer) (command, error) {
	key := args[0] + " " + args[1]
	fs := flag.NewFlagSet("mailctl "+key, flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	var audit internalmail.OperatorAuditInput
	var id, domainID string
	var namespace bool
	expected := int64(-1)
	wait := api.InternalMailDrainWaitTimeout
	cancel := key == "messages cancel" || key == "recipients cancel"
	if cancel {
		fs.StringVar(&id, "id", "", "message or recipient UUID")
	} else {
		fs.BoolVar(&namespace, "namespace", false, "select maild's configured namespace")
		fs.StringVar(&domainID, "domain-id", "", "sending domain UUID")
		if key != "delivery status" {
			fs.Int64Var(&expected, "expected-revision", -1, "scope revision from delivery status")
		}
	}
	if key != "delivery status" {
		addAuditFlags(fs, &audit)
	}
	if key == "delivery drain" {
		fs.DurationVar(&wait, "timeout", api.InternalMailDrainWaitTimeout, "maximum local drain wait (up to 5m)")
	}
	if err := fs.Parse(args[2:]); err != nil {
		return command{}, err
	}
	if fs.NArg() != 0 {
		return command{}, fmt.Errorf("positional arguments are not accepted")
	}
	if key != "delivery status" && audit.Validate() != nil {
		return command{}, fmt.Errorf("valid --request-id, --actor, --reason and --evidence are required")
	}
	if cancel {
		if !canonicalCLIUUID(id) {
			return command{}, fmt.Errorf("--id must be a canonical UUID")
		}
		resource := "messages"
		if key == "recipients cancel" {
			resource = "recipients"
		}
		return command{http.MethodPost, internalmail.OperationsPath + "/" + resource + "/" + id + "/cancel", audit}, nil
	}
	if namespace == (domainID != "") || domainID != "" && !canonicalCLIUUID(domainID) {
		return command{}, fmt.Errorf("choose exactly one of --namespace or --domain-id UUID")
	}
	scope := internalmail.DeliveryScope{Kind: "namespace"}
	if domainID != "" {
		scope = internalmail.DeliveryScope{Kind: "domain", DomainID: domainID}
	}
	if key == "delivery status" {
		return command{http.MethodGet, deliveryStatusRoute(scope), nil}, nil
	}
	if expected < 0 {
		return command{}, fmt.Errorf("--expected-revision is required and must be non-negative")
	}
	input := deliveryCommandInput{DeliveryControlRequest: internalmail.DeliveryControlRequest{OperatorAuditInput: audit, DeliveryScope: scope, ExpectedRevision: &expected}}
	action := strings.TrimPrefix(key, "delivery ")
	if action == "drain" {
		if wait <= 0 || wait > api.InternalMailMaxDrainWaitTimeout {
			return command{}, fmt.Errorf("--timeout must be positive and at most 5m")
		}
		input.wait = wait
	}
	return command{http.MethodPost, internalmail.DeliveryControlsPath + "/" + action, input}, nil
}

func deliveryStatusRoute(scope internalmail.DeliveryScope) string {
	if scope.DomainID == "" {
		return internalmail.DeliveryControlsPath
	}
	return internalmail.DeliveryControlsPath + "?" + url.Values{"domain_id": {scope.DomainID}}.Encode()
}

type drainOutput struct {
	Operation internalmail.OperationRecord       `json:"operation"`
	Status    internalmail.DeliveryControlStatus `json:"status"`
	Complete  bool                               `json:"complete"`
}

func finishControlCommand(ctx context.Context, client *internalmail.OperatorClient, cmd command, raw json.RawMessage) (json.RawMessage, error) {
	input, ok := cmd.input.(deliveryCommandInput)
	if !ok || input.wait == 0 {
		return raw, nil
	}
	var record internalmail.OperationRecord
	if json.Unmarshal(raw, &record) != nil {
		return raw, errors.New("invalid drain operation result; inspect audit and delivery status")
	}
	var initial internalmail.DeliveryControlStatus
	if json.Unmarshal(record.Result, &initial) != nil || initial.Mode != "draining" {
		return raw, errors.New("invalid drain control result; inspect audit and delivery status")
	}
	result, err := waitForDrain(ctx, client, record, initial, input.wait)
	output, encodeErr := json.Marshal(result)
	if encodeErr != nil {
		return raw, encodeErr
	}
	return output, err
}

func waitForDrain(parent context.Context, client *internalmail.OperatorClient, record internalmail.OperationRecord, initial internalmail.DeliveryControlStatus, timeout time.Duration) (drainOutput, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	output := drainOutput{Operation: record, Status: initial}
	ticker := time.NewTicker(api.InternalMailDrainPollInterval)
	defer ticker.Stop()
	for {
		raw, err := client.Request(ctx, http.MethodGet, deliveryStatusRoute(initial.DeliveryScope), nil)
		if err != nil {
			return output, errors.New("drain wait interrupted; the saved hold remains unless another operator changed it")
		}
		var status internalmail.DeliveryControlStatus
		if json.Unmarshal(raw, &status) != nil {
			return output, errors.New("invalid drain status response")
		}
		output.Status = status
		if status.Revision != initial.Revision || status.Mode != "draining" {
			return output, errors.New("drain scope changed; inspect current delivery status")
		}
		if status.Drained {
			output.Complete = true
			return output, nil
		}
		if status.ActiveClaims == 0 && status.ActiveSubmissions == 0 && status.Uncertain > 0 {
			return output, errors.New("drain requires uncertain-submission reconciliation; the hold remains set")
		}
		select {
		case <-ctx.Done():
			return output, errors.New("drain wait expired or was cancelled; the hold remains set")
		case <-ticker.C:
		}
	}
}

func cancellationIncomplete(route string, raw json.RawMessage) bool {
	if !strings.HasSuffix(route, "/cancel") {
		return false
	}
	var record struct {
		Result internalmail.CancellationResult `json:"result"`
	}
	return json.Unmarshal(raw, &record) != nil || !record.Result.Complete
}
