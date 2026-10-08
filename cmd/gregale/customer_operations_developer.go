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
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type customerOperationDeveloperCommand struct {
	verb, action, app, deployment, name, dir, plan, input, definition, key, receipt string
	self                                                                            bool
	timeout                                                                         time.Duration
}

func parseCustomerOperationDeveloper(args []string) (customerOperationDeveloperCommand, error) {
	var c customerOperationDeveloperCommand
	if len(args) == 0 {
		return c, fmt.Errorf("missing developer command")
	}
	c.verb, c.dir = args[0], "."
	args = args[1:]
	if c.verb == "definitions" {
		if len(args) == 0 || (args[0] != "list" && args[0] != "get") {
			return c, fmt.Errorf("definitions requires list or get")
		}
		c.action, args = args[0], args[1:]
	}
	fs := newFlagSet("customer-operations "+c.verb, flag.ContinueOnError)
	if c.verb != "validate" {
		fs.DurationVar(&c.timeout, "timeout", 0, "local request deadline")
	}
	switch c.verb {
	case "definitions":
		fs.StringVar(&c.app, "app", "", "account-owned app")
		fs.StringVar(&c.deployment, "deployment", "", "immutable deployment ID")
	case "validate":
		fs.StringVar(&c.app, "app", "", "selected manifest app")
		fs.StringVar(&c.dir, "dir", ".", "source directory")
		fs.StringVar(&c.plan, "plan", "", "explicit target plan")
		fs.StringVar(&c.name, "name", "", "operation to validate with sample input")
		fs.StringVar(&c.input, "input-file", "", "optional sample JSON input")
	case "start":
		fs.BoolVar(&c.self, "self", false, "derive ownership from a tenant credential")
		fs.StringVar(&c.definition, "definition", "", "immutable definition ID")
		fs.StringVar(&c.input, "input-file", "", "JSON input; saved privately for retries")
		fs.StringVar(&c.key, "idempotency-key", "", "stable logical request key")
		fs.StringVar(&c.receipt, "receipt-file", "", "private, immutable request receipt")
	default:
		return c, fmt.Errorf("unknown developer command")
	}
	if err := parseInterspersed(fs, args); err != nil {
		return c, err
	}
	if c.timeout < 0 {
		return c, fmt.Errorf("--timeout must not be negative")
	}
	expected := 0
	if c.verb == "definitions" && c.action == "get" {
		expected = 1
	}
	if fs.NArg() != expected {
		return c, fmt.Errorf("unexpected positional arguments")
	}
	if expected == 1 {
		c.name = fs.Arg(0)
	}
	if c.verb == "definitions" && (c.app == "" || c.deployment == "") {
		return c, fmt.Errorf("definitions requires --app and --deployment")
	}
	if c.verb == "validate" && (c.app == "" || c.plan == "" || (c.input != "" && c.name == "")) {
		return c, fmt.Errorf("validate requires --app, --plan and --name when sample input is supplied")
	}
	if c.verb == "start" {
		if !c.self || c.receipt == "" {
			return c, fmt.Errorf("start requires --self and --receipt-file")
		}
		if (c.definition != "" || c.input != "" || c.key != "") && (c.definition == "" || c.input == "" || c.key == "") {
			return c, fmt.Errorf("supply --definition, --input-file and --idempotency-key together, or resume from only --receipt-file")
		}
	}
	return c, nil
}

func cmdCustomerOperationDeveloper(args []string) int {
	c, err := parseCustomerOperationDeveloper(args)
	if err != nil {
		return printErr("Invalid customer-operations command", err)
	}
	if c.verb == "validate" {
		report, err := validateCustomerOperationSource(c)
		if err != nil {
			return printErr("Operation validation failed", err)
		}
		if err := renderCustomerOperationValidation(osStdout, report, jsonOutput); err != nil {
			return printErr("Could not write validation", err)
		}
		return 0
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
	if c.verb == "definitions" {
		err = runCustomerOperationDefinitions(ctx, client, c, osStdout, jsonOutput)
	} else {
		var accepted api.OperationAcceptedResponse
		accepted, err = startCustomerOperation(ctx, client, c, time.Now().UTC())
		if err == nil {
			if jsonOutput {
				err = json.NewEncoder(osStdout).Encode(accepted)
			} else {
				_, err = fmt.Fprintf(osStdout, "Operation %s accepted. Request receipt: %s\n", accepted.ID, c.receipt)
			}
		}
	}
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return 130
	}
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
		_ = printErr("Local request timed out; resume with the same receipt", err)
		return 124
	}
	if err != nil {
		return printErr("Customer operation command failed", err)
	}
	return 0
}

type customerOperationDefinitionClient interface {
	ListOperationDefinitions(context.Context, string, string) (api.OperationDefinitionsResponse, error)
	GetOperationDefinition(context.Context, string, string, string) (api.OperationDefinitionResponse, error)
}

func runCustomerOperationDefinitions(ctx context.Context, client customerOperationDefinitionClient, c customerOperationDeveloperCommand, out io.Writer, asJSON bool) error {
	if c.action == "get" {
		definition, err := client.GetOperationDefinition(ctx, c.app, c.deployment, c.name)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(definition)
	}
	page, err := client.ListOperationDefinitions(ctx, c.app, c.deployment)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(out).Encode(page)
	}
	for _, d := range page.Definitions {
		if _, err := fmt.Fprintf(out, "%s\t%s\trevision=%s\tdeployment=%s\tscope=%s\tcompletion=%s\thttp_transaction_version=%d\n", d.ID, d.Name, d.Revision, d.DeploymentID, d.Scope, d.CompletionWebhookID, d.HTTPTransactionVersion); err != nil {
			return err
		}
	}
	return nil
}
