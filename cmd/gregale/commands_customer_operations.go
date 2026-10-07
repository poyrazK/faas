package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

type customerOperationsClient interface {
	ListAccountOperations(context.Context, string, api.OperationListOptions) (api.OperationListResponse, error)
	GetOperation(context.Context, string, string) (api.OperationResponse, error)
	GetAccountOperationEvents(context.Context, string, string, int64) (api.OperationEventsResponse, error)
	GetOperationExecutions(context.Context, string, string, int, int) (api.OperationExecutionsResponse, error)
	CancelOperation(context.Context, string, string, api.OperationCancellationRequest) (api.OperationResponse, error)
	RecoverOperation(context.Context, string, string, api.OperationRecoveryRequest) (api.OperationResponse, error)
	RetryOperationDelivery(context.Context, string, string) (api.OperationResponse, error)
	DownloadOperationArtifact(context.Context, string, string, string, io.Writer) (int64, error)
}

type customerOperationCommand struct {
	verb, app, id, scope, tenant, name, state, cursor, artifact, output string
	after                                                               int
	limit                                                               int
	timeout, interval                                                   time.Duration
	recovery                                                            api.OperationRecoveryRequest
	self                                                                bool
	preview                                                             bool
	receipt                                                             string
	recoveryFlags                                                       map[string]bool
}

func cmdCustomerOperations(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale customer-operations <doctor|definitions|validate|types|start|list|get|inspect|events|executions|watch|download|cancel|recover|delivery|delivery-attempts|retry-delivery>", "customer-operations")
		return 1
	}
	if len(args) > 0 && (args[0] == "delivery" || args[0] == "delivery-attempts" || args[0] == "retry-delivery") {
		return cmdCustomerOperationDelivery(args)
	}
	if len(args) > 0 && args[0] == "doctor" {
		return cmdCustomerOperationDoctor(args[1:])
	}
	if len(args) > 0 && (args[0] == "definitions" || args[0] == "validate" || args[0] == "types" || args[0] == "start") {
		return cmdCustomerOperationDeveloper(args)
	}
	command, err := parseCustomerOperationCommand(args)
	if err != nil {
		return printErr("Invalid customer-operations command", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if command.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, command.timeout)
		defer cancel()
	}
	var scoped customerOperationsClient = client
	if command.self {
		scoped = tenantCustomerOperationsClient{client: client}
	}
	code, err := runCustomerOperationCommand(ctx, scoped, command, osStdout, jsonOutput)
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return 130
	}
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
		_ = printErr("Customer operation wait timed out; work may continue", err)
		return 124
	}
	if err != nil {
		return printErr("Customer operation command failed", err)
	}
	return code
}

func parseCustomerOperationCommand(args []string) (customerOperationCommand, error) {
	var c customerOperationCommand
	if len(args) == 0 {
		return c, fmt.Errorf("usage: gregale customer-operations <list|get|inspect|events|executions|watch|download|cancel|recover|retry-delivery> --app SLUG")
	}
	c.verb = args[0]
	fs := newFlagSet("customer-operations "+c.verb, flag.ContinueOnError)
	fs.StringVar(&c.app, "app", "", "account-owned application slug")
	fs.BoolVar(&c.self, "self", false, "use only the authenticated tenant's routes")
	fs.DurationVar(&c.timeout, "timeout", 0, "stop the local request/wait after this duration; work continues")
	var evidenceFile, resultFile string
	switch c.verb {
	case "list":
		fs.StringVar(&c.scope, "scope", "", "explicit deployment environment")
		fs.StringVar(&c.tenant, "tenant", "", "optional platform tenant UUID")
		fs.StringVar(&c.name, "name", "", "operation definition name")
		fs.StringVar(&c.state, "state", "", "business state filter")
		fs.StringVar(&c.cursor, "cursor", "", "next page cursor")
		fs.IntVar(&c.limit, "limit", api.OperationHistoryPageDefault, "page size")
	case "events", "executions":
		fs.IntVar(&c.after, "after", 0, "event sequence or execution generation watermark")
		if c.verb == "executions" {
			fs.IntVar(&c.limit, "limit", api.OperationHistoryPageDefault, "page size")
		}
	case "watch":
		fs.DurationVar(&c.interval, "interval", time.Second, "status polling interval")
	case "download":
		fs.StringVar(&c.artifact, "artifact", "", "artifact ID; may be omitted when exactly one exists")
		fs.StringVar(&c.output, "output", "", "new local file path; existing files are preserved")
	case "cancel":
		fs.IntVar(&c.recovery.ExpectedGeneration, "expected-generation", 0, "observed operation generation")
	case "recover":
		fs.StringVar(&c.receipt, "receipt-file", "", "private immutable recovery request; omit selectors to resume")
		fs.BoolVar(&c.preview, "preview", false, "read the recovery plan without recording or executing a decision")
		fs.StringVar(&c.recovery.ExpectedInspectionRevision, "inspection-revision", "", "optional revision from inspection or preview; reject changed execution evidence")
		fs.IntVar(&c.recovery.ExpectedGeneration, "expected-generation", 0, "observed operation generation")
		fs.StringVar(&c.recovery.RecoveryID, "recovery-id", "", "stable ID for this recovery decision")
		fs.StringVar(&c.recovery.Resolution, "resolution", "", "succeeded, failed, cancelled or safe_to_retry")
		fs.StringVar(&evidenceFile, "evidence-file", "", "file containing reconciliation evidence")
		fs.StringVar(&resultFile, "result-file", "", "JSON result required for succeeded")
	case "get", "inspect", "retry-delivery":
	default:
		return c, fmt.Errorf("unknown customer-operations command %q", c.verb)
	}
	flags, positionals := splitArgsForFlags(args[1:], "self", "preview")
	if err := fs.Parse(flags); err != nil {
		return c, err
	}
	expected := 1
	if c.verb == "list" {
		expected = 0
	}
	if len(positionals) != expected || fs.NArg() != 0 || (!c.self && strings.TrimSpace(c.app) == "") || c.timeout < 0 {
		return c, fmt.Errorf("%s requires --app and %d operation ID positional argument(s)", c.verb, expected)
	}
	if c.self {
		switch c.verb {
		case "get", "events", "watch", "download", "cancel":
		default:
			return c, fmt.Errorf("--self supports get, events, watch, download and cancel")
		}
		if c.app != "" {
			return c, fmt.Errorf("--self derives ownership from credentials; omit --app")
		}
	}
	if expected == 1 {
		c.id = positionals[0]
		if strings.TrimSpace(c.id) == "" {
			return c, fmt.Errorf("operation ID must not be empty")
		}
	}
	if c.verb == "list" && api.ValidateScope(c.scope) != nil {
		return c, fmt.Errorf("list requires a valid explicit --scope")
	}
	if c.verb == "watch" && c.interval <= 0 {
		return c, fmt.Errorf("--interval must be positive")
	}
	if c.after < 0 || ((c.verb == "list" || c.verb == "executions") && (c.limit < 1 || c.limit > api.OperationHistoryPageMax)) {
		return c, fmt.Errorf("invalid pagination bounds")
	}
	if c.verb == "download" && c.output == "" {
		return c, fmt.Errorf("download requires --output")
	}
	c.recoveryFlags = map[string]bool{}
	fs.Visit(func(f *flag.Flag) { c.recoveryFlags[f.Name] = true })
	if (c.verb == "cancel" || c.verb == "recover" && (c.receipt == "" || c.preview || c.recoveryFlags["expected-generation"])) && c.recovery.ExpectedGeneration < 1 {
		return c, fmt.Errorf("--expected-generation must be positive")
	}
	if c.verb == "recover" {
		var err error
		if c.preview && c.receipt != "" {
			return c, fmt.Errorf("--preview cannot create or apply a --receipt-file")
		}
		if c.receipt != "" {
			if !validCustomerOperationUUID(c.id) {
				return c, fmt.Errorf("recovery receipt requires a valid operation UUID")
			}
			err = loadCustomerOperationRecoveryReceiptFlags(&c, evidenceFile, resultFile)
		} else if c.preview {
			err = loadCustomerOperationPreview(&c.recovery, evidenceFile, resultFile)
		} else {
			err = loadCustomerOperationRecovery(&c.recovery, evidenceFile, resultFile)
		}
		if err != nil {
			return c, err
		}
	}
	return c, nil
}

func readCustomerOperationFile(path string, limit int64) ([]byte, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return nil, fmt.Errorf("open operation input: %w", err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read operation input: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("operation input exceeds %d bytes", limit)
	}
	return data, nil
}

func loadCustomerOperationRecovery(req *api.OperationRecoveryRequest, evidenceFile, resultFile string) error {
	if req.RecoveryID == "" || len(req.RecoveryID) > api.OperationIdempotencyKeyMaxBytes || strings.ContainsAny(req.RecoveryID, "\r\n\x00") || evidenceFile == "" {
		return fmt.Errorf("recover requires a stable --recovery-id and --evidence-file")
	}
	switch req.Resolution {
	case "succeeded", "failed", "cancelled", "safe_to_retry":
	default:
		return fmt.Errorf("invalid --resolution")
	}
	evidence, err := readCustomerOperationFile(evidenceFile, api.OperationRecoveryEvidenceMaxBytes)
	if err != nil {
		return err
	}
	if !utf8.Valid(evidence) || strings.TrimSpace(string(evidence)) == "" || strings.ContainsRune(string(evidence), 0) {
		return fmt.Errorf("reconciliation evidence must be nonempty text")
	}
	req.Evidence = string(evidence)
	if req.Resolution == "succeeded" {
		if resultFile == "" {
			return fmt.Errorf("succeeded requires --result-file")
		}
		req.Result, err = readCustomerOperationFile(resultFile, api.OperationSubmissionMaxBytes)
		if err != nil {
			return err
		}
		if !json.Valid(req.Result) {
			return fmt.Errorf("result file must contain one JSON value")
		}
	} else if resultFile != "" {
		return fmt.Errorf("only succeeded accepts --result-file")
	}
	return nil
}

func runCustomerOperationCommand(ctx context.Context, client customerOperationsClient, c customerOperationCommand, out io.Writer, asJSON bool) (int, error) {
	if c.verb == "recover" && c.receipt != "" {
		return runCustomerOperationRecoveryReceipt(ctx, client, c, out, asJSON, time.Now().UTC())
	}
	if c.verb == "inspect" || c.verb == "recover" && c.preview {
		return runCustomerOperationRecoveryRead(ctx, client, c, out, asJSON)
	}
	switch c.verb {
	case "list":
		page, err := client.ListAccountOperations(ctx, c.app, api.OperationListOptions{Scope: c.scope, TenantID: c.tenant, Name: c.name, State: api.OperationState(c.state), Limit: c.limit, Cursor: c.cursor})
		if err != nil {
			return 0, err
		}
		if asJSON {
			return 0, json.NewEncoder(out).Encode(page)
		}
		for _, op := range page.Operations {
			if _, err := fmt.Fprintf(out, "%s\ttenant=%s\t%s\twork=%s\tdelivery=%s\tgeneration=%d\n", op.ID, op.PlatformTenantID, op.Name, op.State, op.CompletionDelivery.State, op.Generation); err != nil {
				return 0, err
			}
		}
		if page.NextCursor != "" {
			_, err = fmt.Fprintf(out, "Next page: --cursor %s\n", page.NextCursor)
		}
		return 0, err
	case "events":
		page, err := client.GetAccountOperationEvents(ctx, c.app, c.id, int64(c.after))
		if err != nil {
			return 0, err
		}
		// JSON is also the default for event/execution evidence; no lossy text projection.
		return 0, json.NewEncoder(out).Encode(page)
	case "executions":
		page, err := client.GetOperationExecutions(ctx, c.app, c.id, c.after, c.limit)
		if err != nil {
			return 0, err
		}
		return 0, json.NewEncoder(out).Encode(page)
	case "watch":
		return watchCustomerOperation(ctx, client, c, out, asJSON)
	case "download":
		receipt, err := downloadCustomerOperation(ctx, client, c)
		if err != nil {
			return 0, err
		}
		if asJSON {
			return 0, json.NewEncoder(out).Encode(receipt)
		}
		_, err = fmt.Fprintf(out, "Downloaded %d verified bytes to %s\n", receipt.SizeBytes, receipt.Path)
		return 0, err
	}
	var op api.OperationResponse
	var err error
	switch c.verb {
	case "get":
		op, err = client.GetOperation(ctx, c.app, c.id)
	case "cancel":
		op, err = client.CancelOperation(ctx, c.app, c.id, api.OperationCancellationRequest{ExpectedGeneration: c.recovery.ExpectedGeneration})
	case "recover":
		op, err = client.RecoverOperation(ctx, c.app, c.id, c.recovery)
	case "retry-delivery":
		op, err = client.RetryOperationDelivery(ctx, c.app, c.id)
	default:
		return 0, fmt.Errorf("unknown customer operation command")
	}
	if err != nil {
		return 0, err
	}
	return 0, renderCustomerOperation(out, op, asJSON)
}

func renderCustomerOperation(out io.Writer, op api.OperationResponse, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(out).Encode(op)
	}
	_, err := fmt.Fprintf(out, "%s\twork=%s\tdelivery=%s\tdelivery_attempts=%d\tgeneration=%d\tsequence=%d", op.ID, op.State, op.CompletionDelivery.State, op.CompletionDelivery.Attempts, op.Generation, op.LatestSequence)
	if err != nil {
		return err
	}
	if op.Progress != nil {
		if _, err = fmt.Fprintf(out, "\tprogress=%s %d/%d", op.Progress.Stage, op.Progress.Completed, op.Progress.Total); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintln(out)
	return err
}

// Watching reports changed durable snapshots as NDJSON in JSON mode. A failed
// notification never changes a successful work exit; uncertain effects stop
// with exit 4, requiring an explicit operator reconciliation decision.
func watchCustomerOperation(ctx context.Context, client customerOperationsClient, c customerOperationCommand, out io.Writer, asJSON bool) (int, error) {
	var previous string
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		op, err := client.GetOperation(ctx, c.app, c.id)
		if err != nil {
			return 0, err
		}
		snapshot, err := json.Marshal(op)
		if err != nil {
			return 0, err
		}
		if string(snapshot) != previous {
			if err := renderCustomerOperation(out, op, asJSON); err != nil {
				return 0, err
			}
			previous = string(snapshot)
		}
		if op.State == api.OperationRequiresReconciliation {
			return 4, nil
		}
		if op.State.Terminal() {
			if op.State == api.OperationSucceeded {
				return 0, nil
			}
			return 1, nil
		}
		timer := time.NewTimer(c.interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, ctx.Err()
		case <-timer.C:
		}
	}
}

type customerOperationDownloadReceipt struct {
	OperationID string `json:"operation_id"`
	ArtifactID  string `json:"artifact_id"`
	Path        string `json:"path"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
}

func downloadCustomerOperation(ctx context.Context, client customerOperationsClient, c customerOperationCommand) (customerOperationDownloadReceipt, error) {
	var receipt customerOperationDownloadReceipt
	op, err := client.GetOperation(ctx, c.app, c.id)
	if err != nil {
		return receipt, err
	}
	var artifact api.OperationResultArtifact
	for _, a := range op.Artifacts {
		if a.ID == c.artifact || (c.artifact == "" && len(op.Artifacts) == 1) {
			artifact = a
			break
		}
	}
	if artifact.ID == "" {
		return receipt, fmt.Errorf("select an existing --artifact ID; omission requires exactly one artifact")
	}
	path, err := filepath.Abs(c.output)
	if err != nil {
		return receipt, fmt.Errorf("resolve output path: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".gregale-operation-*")
	if err != nil {
		return receipt, fmt.Errorf("create private download file: %w", err)
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	hash := sha256.New()
	n, err := client.DownloadOperationArtifact(ctx, c.app, c.id, artifact.ID, io.MultiWriter(file, hash))
	if err != nil {
		return receipt, err
	}
	digest := fmt.Sprintf("sha256:%x", hash.Sum(nil))
	if n != artifact.SizeBytes || digest != artifact.SHA256 {
		return receipt, fmt.Errorf("download does not match retained artifact size and digest")
	}
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	if err := file.Sync(); err != nil {
		return receipt, fmt.Errorf("flush verified download: %w", err)
	}
	if err := file.Close(); err != nil {
		return receipt, fmt.Errorf("close verified download: %w", err)
	}
	// Publishing a hard link is atomic and refuses existing files, including
	// symlinks. The private temporary file lives on the destination filesystem.
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	if err := os.Link(file.Name(), path); err != nil {
		return receipt, fmt.Errorf("publish verified download (output must be new): %w", err)
	}
	return customerOperationDownloadReceipt{OperationID: op.ID, ArtifactID: artifact.ID, Path: path, SizeBytes: n, SHA256: digest}, nil
}
