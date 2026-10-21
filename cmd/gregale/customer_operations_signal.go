package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-1197: deliver a named event to one Operation, or to every active
// Operation of a definition about one business subject.
type customerOperationSignalClient interface {
	SignalOperation(context.Context, string, string, api.OperationSignalRequest) (api.OperationSignalReceipt, error)
	SignalOperationsBySubject(context.Context, string, api.OperationSubjectSignalRequest) (api.OperationSubjectSignalResponse, error)
}

type customerOperationSignalCommand struct {
	app, id, scope, operation, subject string
	// ADR-20261017100853: --self signals the tenant's own Operations; a subject signal
	// then names the definition instead of scope and operation.
	self       bool
	definition string
	timeout    time.Duration
	request    api.OperationSignalRequest
}

func parseCustomerOperationSignal(args []string) (customerOperationSignalCommand, error) {
	var c customerOperationSignalCommand
	var payload, payloadFile string
	fs := newFlagSet("customer-operations signal", flag.ContinueOnError)
	fs.StringVar(&c.app, "app", "", "account-owned application")
	fs.DurationVar(&c.timeout, "timeout", 0, "local request deadline")
	fs.StringVar(&c.request.Event, "event", "", "event name the operation waits for")
	fs.StringVar(&c.request.SignalID, "signal-id", "", "stable idempotency key for this delivery")
	fs.StringVar(&payload, "payload", "", "JSON payload")
	fs.StringVar(&payloadFile, "payload-file", "", "file holding the JSON payload")
	fs.StringVar(&c.scope, "scope", "", "environment, with --operation and --subject")
	fs.StringVar(&c.operation, "operation", "", "operation name, with --scope and --subject")
	fs.StringVar(&c.subject, "subject", "", "business subject as TYPE:ID, with --scope and --operation")
	fs.BoolVar(&c.self, "self", false, "signal the authenticated tenant's own operations")
	fs.StringVar(&c.definition, "definition", "", "operation definition ID, with --self and --subject")
	if err := parseInterspersed(fs, args); err != nil {
		return c, err
	}
	if c.request.Event == "" || c.request.SignalID == "" || c.timeout < 0 {
		return c, fmt.Errorf("signal requires --event and --signal-id")
	}
	bySubject := c.scope != "" || c.operation != "" || c.subject != "" || c.definition != ""
	switch {
	case c.self && (c.app != "" || c.scope != "" || c.operation != ""):
		return c, fmt.Errorf("--self derives ownership from credentials; omit --app, and name a subject signal's --definition instead of --scope and --operation")
	case !c.self && (c.app == "" || c.definition != ""):
		return c, fmt.Errorf("signal requires --app; --definition needs --self")
	case c.self && bySubject && (fs.NArg() != 0 || !validCustomerOperationUUID(c.definition) || !strings.Contains(c.subject, ":")):
		return c, fmt.Errorf("a --self subject signal takes --definition UUID and --subject TYPE:ID instead of an operation ID")
	case !c.self && bySubject && (fs.NArg() != 0 || c.scope == "" || c.operation == "" || !strings.Contains(c.subject, ":")):
		return c, fmt.Errorf("a subject signal takes --scope, --operation and --subject TYPE:ID instead of an operation ID")
	case !bySubject && (fs.NArg() != 1 || !validCustomerOperationUUID(fs.Arg(0))):
		return c, fmt.Errorf("signal requires an operation UUID, or --scope, --operation and --subject")
	case !bySubject:
		c.id = fs.Arg(0)
	}
	if payload != "" && payloadFile != "" {
		return c, fmt.Errorf("pass --payload or --payload-file, not both")
	}
	if payloadFile != "" {
		raw, err := os.ReadFile(payloadFile) //nolint:gosec // the operator names the payload file
		if err != nil {
			return c, err
		}
		payload = string(raw)
	}
	if payload != "" {
		if len(payload) > api.OperationSignalPayloadMaxBytes || !json.Valid([]byte(payload)) {
			return c, fmt.Errorf("payload must be JSON of at most %d bytes", api.OperationSignalPayloadMaxBytes)
		}
		c.request.Payload = json.RawMessage(payload)
	}
	return c, nil
}

func cmdCustomerOperationSignal(args []string) int {
	c, err := parseCustomerOperationSignal(args)
	if err != nil {
		return printErr("Invalid signal command", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	var scoped customerOperationSignalClient = client
	if c.self {
		scoped = tenantCustomerOperationSignalClient{client: client, definition: c.definition}
	}
	err = runCustomerOperationSignal(ctx, scoped, c, osStdout, jsonOutput)
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return 130
	}
	if err != nil {
		return printErr("Signal failed; repeat the same --signal-id to retry safely", err)
	}
	return 0
}

func runCustomerOperationSignal(ctx context.Context, client customerOperationSignalClient, c customerOperationSignalCommand, out io.Writer, asJSON bool) error {
	var receipts []api.OperationSignalReceipt
	var result any
	if c.id != "" {
		receipt, err := client.SignalOperation(ctx, c.app, c.id, c.request)
		if err != nil {
			return err
		}
		receipts, result = []api.OperationSignalReceipt{receipt}, receipt
	} else {
		kind, id, _ := strings.Cut(c.subject, ":")
		page, err := client.SignalOperationsBySubject(ctx, c.app, api.OperationSubjectSignalRequest{Scope: c.scope, Operation: c.operation,
			Subject: api.OperationSubject{Type: kind, ID: id}, SignalID: c.request.SignalID, Event: c.request.Event, Payload: c.request.Payload})
		if err != nil {
			return err
		}
		receipts, result = page.Delivered, page
	}
	if asJSON {
		return json.NewEncoder(out).Encode(result)
	}
	if len(receipts) == 0 {
		_, err := fmt.Fprintln(out, "No active operation matched; nothing was delivered.")
		return err
	}
	for _, r := range receipts {
		state := "held until its wait starts"
		if r.Waiting {
			state = "resumed its wait"
		}
		if r.Duplicate {
			state = "already delivered"
		}
		if _, err := fmt.Fprintf(out, "%s\t%s\t%s\n", r.OperationID, r.Event, state); err != nil {
			return err
		}
	}
	return nil
}

// tenantCustomerOperationSignalClient selects only the tenant-self signal
// routes (ADR-20261017100853); a subject signal is scoped by its definition.
type tenantCustomerOperationSignalClient struct {
	client     *api.Client
	definition string
}

func (c tenantCustomerOperationSignalClient) SignalOperation(ctx context.Context, _, id string, req api.OperationSignalRequest) (api.OperationSignalReceipt, error) {
	return c.client.SignalPlatformTenantSelfOperation(ctx, id, req)
}

func (c tenantCustomerOperationSignalClient) SignalOperationsBySubject(ctx context.Context, _ string, req api.OperationSubjectSignalRequest) (api.OperationSubjectSignalResponse, error) {
	return c.client.SignalPlatformTenantSelfOperationsBySubject(ctx, api.PlatformTenantSubjectSignalRequest{DefinitionID: c.definition,
		Subject: req.Subject, SignalID: req.SignalID, Event: req.Event, Payload: req.Payload})
}
