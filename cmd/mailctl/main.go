// Command mailctl operates only on maild's private Unix socket. It has no
// customer API, SMTP transport, database access, or encryption key access.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

const usage = `Usage: mailctl <resource> <command> [flags]
  messages list       [--status STATE] [--limit N] [--after CURSOR]
  messages show       --id UUID
  messages events     --id UUID [--limit N] [--after CURSOR]
  domains list        [--limit N] [--after CURSOR]
  domains show        --id UUID
  domains check       --id UUID   (DNS observation, does not change status)
  domains keys        --id UUID [--limit N] [--after CURSOR]
  domains stage-key   --id UUID --request-id UUID --actor NAME --reason SLUG --evidence REF
  domains verify-key  --id UUID --key-id UUID (same audit flags)
  domains activate-key --id UUID --key-id UUID (same audit flags)
  domains retire-key  --id UUID --key-id UUID --confirmed-queue-drained (same audit flags)
  domains reverify    --id UUID (same audit flags)
  suppressions list   [--limit N] [--after CURSOR]   (read only)
  suppressions show   --address MAILBOX
  suppressions history --address MAILBOX [--limit 25] [--after REVISION]
  suppressions add    --address MAILBOX --expected-revision N (audit flags)
  suppressions release --address MAILBOX --expected-revision N --confirmed-reviewed (audit flags)
  audit list          [--limit N] [--after CURSOR]
  relay status        (read-only automatic relay protection status)
  scheduler status    (read-only producer scheduling and dispatch limits)
  producers status    --producer ID
  producers rotate    --producer ID --replacement-id ID [--overlap 1h]
                      --expected-revision N (audit flags)
  producers revoke    --producer ID --credential-id ID --expected-revision N (audit flags)
  producers suspend   --producer ID --expected-revision N (audit flags)
  producers resume    --producer ID --expected-revision N --confirmed-reviewed (audit flags)
  outbox status       (read-only producer handoff counts)
  outbox list         [--status STATE] [--limit 25] [--after UUID]
  outbox show         --id UUID
  outbox admission    --id UUID (read-only admission evidence and reconciliation blockers)
  recovery status
  recovery preview    [--limit 25] [--after UUID]
  recovery review     --id UUID --recovery-id UUID --expected-revision N
                      --expected-status STATE --outcome retry|submitted
                      --confirmed-not-accepted OR --confirmed-relay-accepted (audit flags)
  recovery release    --recovery-id UUID --expected-revision N --confirmed-policy
                      --confirmed-access --confirmed-consumers --confirmed-relay
                      --confirmed-quarantine (audit flags)
  encryption status   (key IDs and retained dependencies; no private material)
  encryption activate --key-id ID --expected-revision N (audit flags)
  encryption preview  [--batch-size 10]
  encryption rewrap   --key-id ID --expected-revision N [--batch-size 10] (audit flags)
  encryption retire   --key-id ID --expected-revision N (audit flags)
  relay events        [--limit N] [--after CURSOR]
  uncertain resolve   --id RECIPIENT_UUID --outcome retry|submitted
                      --confirmed-not-accepted OR --confirmed-relay-accepted
                      --request-id UUID --actor NAME --reason SLUG --evidence REF
  retention preview
  retention apply     --request-id UUID --actor NAME --reason SLUG --evidence REF
  delivery status     --namespace OR --domain-id UUID
  reputation status   --domain-id UUID OR --producer ID OR --legacy
  events consumers    --producer ID [--limit 25] [--after NAME]
  events status       --producer ID --consumer NAME
  events replay       --producer ID --consumer NAME --from-start OR --after UUID
                      --expected-generation N (audit flags)
  reputation holds    [--limit 25] [--after CURSOR]
  reputation events   [--limit 25] [--after UUID]
  reputation release  --domain-id UUID OR --producer ID OR --legacy
                      --expected-revision N --confirmed-reviewed (audit flags)
  delivery pause      --namespace OR --domain-id UUID --expected-revision N (audit flags)
  delivery resume     --namespace OR --domain-id UUID --expected-revision N (audit flags)
  delivery drain      --namespace OR --domain-id UUID --expected-revision N (audit flags)
                      [--timeout 30s] (holds queued mail and waits for local activity)
  messages cancel     --id MESSAGE_UUID (audit flags)
  recipients cancel   --id RECIPIENT_UUID (audit flags)

Output is JSON. Lists use ascending IDs (addresses for suppressions), not time.
Both master and operations flags must be exact 1 in the private operator environment.
Writes require an explicit, saved request ID. Reuse it with identical arguments
after a lost response; inspect audit before starting a different operation.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Getenv, os.Args[1:], os.Stdout, os.Stderr))
}

type command struct {
	method, route string
	input         any
}

func run(ctx context.Context, getenv func(string) string, args []string, out, diagnostics io.Writer) int {
	if len(args) == 0 || len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		_, _ = io.WriteString(out, usage)
		return 0
	}
	if !internalmail.OperationsEnabled(getenv) {
		_, _ = fmt.Fprintln(diagnostics, "mailctl: internal mail operations disabled")
		return 1
	}
	cmd, err := parseCommand(args, diagnostics)
	if err != nil {
		_, _ = fmt.Fprintln(diagnostics, "mailctl:", err)
		return 2
	}
	if !domainCLIEnabled(cmd.route, getenv) {
		_, _ = fmt.Fprintln(diagnostics, "mailctl: domain lifecycle feature disabled")
		return 1
	}
	if controlCLIRoute(cmd.route) && !internalmail.DeliveryControlsEnabled(getenv) {
		_, _ = fmt.Fprintln(diagnostics, "mailctl: delivery controls disabled")
		return 1
	}
	if (cmd.route == internalmail.RelayOperationsPath || strings.HasPrefix(cmd.route, internalmail.RelayOperationsPath+"/")) && !internalmail.BackpressureEnabled(getenv) {
		_, _ = fmt.Fprintln(diagnostics, "mailctl: relay backpressure disabled")
		return 1
	}
	if reputationCLIRoute(cmd.route) && !internalmail.ReputationEnabled(getenv) {
		_, _ = fmt.Fprintln(diagnostics, "mailctl: reputation protection disabled")
		return 1
	}
	if eventFeedCLIRoute(cmd.route) && !internalmail.EventFeedEnabled(getenv) {
		_, _ = fmt.Fprintln(diagnostics, "mailctl: event feeds disabled")
		return 1
	}
	if cmd.route == internalmail.ProducerSchedulerPath && !internalmail.ProducerSchedulingEnabled(getenv) {
		_, _ = fmt.Fprintln(diagnostics, "mailctl: producer scheduling disabled")
		return 1
	}
	if producerLifecycleCLIRoute(cmd.route) && !internalmail.ProducerLifecycleEnabled(getenv) {
		_, _ = fmt.Fprintln(diagnostics, "mailctl: producer lifecycle disabled")
		return 1
	}
	if encryptionCLIRoute(cmd.route) && !internalmail.EncryptionRotationEnabled(getenv) {
		_, _ = fmt.Fprintln(diagnostics, "mailctl: encryption rotation disabled")
		return 1
	}
	if producerOutboxCLIRoute(cmd.route) && !internalmail.ProducerOutboxEnabled(getenv) {
		_, _ = fmt.Fprintln(diagnostics, "mailctl: producer outbox inspection disabled")
		return 1
	}
	if outboxAdmissionCLIRoute(cmd.route) && !internalmail.OutboxReconciliationEnabled(getenv) {
		_, _ = fmt.Fprintln(diagnostics, "mailctl: outbox reconciliation disabled")
		return 1
	}
	if recoveryCLIRoute(cmd.route) && !internalmail.RecoveryEnabled(getenv) {
		_, _ = fmt.Fprintln(diagnostics, "mailctl: recovery controls disabled")
		return 1
	}
	if suppressionCLIRoute(cmd.route) && !internalmail.SuppressionControlsEnabled(getenv) {
		_, _ = fmt.Fprintln(diagnostics, "mailctl: suppression controls disabled")
		return 1
	}
	socket := getenv("FAAS_INTERNAL_MAIL_SOCKET")
	if socket == "" {
		socket = internalmail.DefaultSocket
	}
	client, err := internalmail.NewOperatorClient(socket, getenv("FAAS_INTERNAL_MAIL_OPERATIONS_TOKEN"), func() bool {
		return internalmail.OperationsEnabled(getenv) && (!reputationCLIRoute(cmd.route) || internalmail.ReputationEnabled(getenv)) && (!eventFeedCLIRoute(cmd.route) || internalmail.EventFeedEnabled(getenv)) && (cmd.route != internalmail.ProducerSchedulerPath || internalmail.ProducerSchedulingEnabled(getenv)) && (!producerLifecycleCLIRoute(cmd.route) || internalmail.ProducerLifecycleEnabled(getenv)) && (!encryptionCLIRoute(cmd.route) || internalmail.EncryptionRotationEnabled(getenv)) && (!producerOutboxCLIRoute(cmd.route) || internalmail.ProducerOutboxEnabled(getenv)) && (!outboxAdmissionCLIRoute(cmd.route) || internalmail.OutboxReconciliationEnabled(getenv)) && (!recoveryCLIRoute(cmd.route) || internalmail.RecoveryEnabled(getenv)) && (!suppressionCLIRoute(cmd.route) || internalmail.SuppressionControlsEnabled(getenv))
	})
	if err != nil {
		_, _ = fmt.Fprintln(diagnostics, "mailctl: invalid private operator configuration")
		return 1
	}
	raw, err := client.Request(ctx, cmd.method, cmd.route, cmd.input)
	if err != nil {
		_, _ = fmt.Fprintln(diagnostics, "mailctl:", err)
		return 1
	}
	raw, completionErr := finishControlCommand(ctx, client, cmd, raw)
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, raw, "", "  "); err != nil {
		_, _ = fmt.Fprintln(diagnostics, "mailctl: invalid JSON response")
		return 1
	}
	formatted.WriteByte('\n')
	if _, err := out.Write(formatted.Bytes()); err != nil {
		return 1
	}
	if completionErr != nil {
		_, _ = fmt.Fprintln(diagnostics, "mailctl:", completionErr)
		return 1
	}
	if cancellationIncomplete(cmd.route, raw) {
		return 1
	}
	if domainObservationFailed(cmd.route, raw) {
		return 1
	}
	return 0
}

func parseCommand(args []string, diagnostics io.Writer) (command, error) {
	if len(args) < 2 {
		return command{}, fmt.Errorf("resource and command required; use mailctl help")
	}
	key := args[0] + " " + args[1]
	if args[0] == "outbox" {
		return parseProducerOutboxCommand(args, diagnostics)
	}
	if args[0] == "recovery" {
		return parseRecoveryCommand(args, diagnostics)
	}
	if args[0] == "suppressions" && args[1] != "list" {
		return parseSuppressionCommand(args, diagnostics)
	}
	if args[0] == "encryption" {
		return parseEncryptionCommand(args, diagnostics)
	}
	if args[0] == "producers" {
		return parseProducerLifecycleCommand(args, diagnostics)
	}
	if args[0] == "events" {
		return parseEventFeedCommand(args, diagnostics)
	}
	if args[0] == "reputation" {
		return parseReputationCommand(args, diagnostics)
	}
	if controlsCommand(key) {
		return parseControlCommand(args, diagnostics)
	}
	if domainLifecycleCommand(key) {
		return parseDomainCommand(args, diagnostics)
	}
	fs := flag.NewFlagSet("mailctl "+key, flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	var id, after, status, outcome string
	limit := api.InternalMailOperatorPageSize
	audit := internalmail.OperatorAuditInput{}
	notAccepted, relayAccepted := false, false
	switch key {
	case "scheduler status":
		if len(args) != 2 {
			return command{}, fmt.Errorf("scheduler status accepts no arguments")
		}
		return command{http.MethodGet, internalmail.ProducerSchedulerPath, nil}, nil
	case "messages list":
		fs.StringVar(&status, "status", "", "recipient state filter")
		fallthrough
	case "domains list", "suppressions list", "audit list", "relay events":
		addPageFlags(fs, &limit, &after)
	case "messages events":
		addPageFlags(fs, &limit, &after)
		fallthrough
	case "messages show", "domains show", "domains check":
		fs.StringVar(&id, "id", "", "resource UUID")
	case "uncertain resolve":
		fs.StringVar(&id, "id", "", "recipient UUID")
		fs.StringVar(&outcome, "outcome", "", "retry or submitted")
		fs.BoolVar(&notAccepted, "confirmed-not-accepted", false, "operator evidence confirms relay did not accept")
		fs.BoolVar(&relayAccepted, "confirmed-relay-accepted", false, "operator evidence confirms durable relay custody")
		addAuditFlags(fs, &audit)
	case "retention apply":
		addAuditFlags(fs, &audit)
	case "retention preview", "relay status":
	default:
		return command{}, fmt.Errorf("unknown command; use mailctl help")
	}
	if err := fs.Parse(args[2:]); err != nil {
		return command{}, err
	}
	if fs.NArg() != 0 {
		return command{}, fmt.Errorf("positional arguments are not accepted")
	}
	return buildCommand(key, id, limit, after, status, audit, internalmail.ReconcileRequest{OperatorAuditInput: audit, Outcome: outcome, ConfirmedNotAccepted: notAccepted, ConfirmedRelayAccepted: relayAccepted})
}

func addPageFlags(fs *flag.FlagSet, limit *int, after *string) {
	fs.IntVar(limit, "limit", api.InternalMailOperatorPageSize, "page size (1..100)")
	fs.StringVar(after, "after", "", "next cursor from the previous page")
}
func addAuditFlags(fs *flag.FlagSet, a *internalmail.OperatorAuditInput) {
	fs.StringVar(&a.RequestID, "request-id", "", "saved UUID for replay safety")
	fs.StringVar(&a.Actor, "actor", "", "operator identity assertion")
	fs.StringVar(&a.Reason, "reason", "", "audit reason slug")
	fs.StringVar(&a.Evidence, "evidence", "", "reviewed evidence reference; never message contents")
}

func buildCommand(key, id string, limit int, after, status string, audit internalmail.OperatorAuditInput, reconcile internalmail.ReconcileRequest) (command, error) {
	query := url.Values{}
	if limit < 1 || limit > api.InternalMailMaxOperatorPageSize || len(after) > api.InternalMailMaxOperatorCursorBytes {
		return command{}, fmt.Errorf("invalid page bounds")
	}
	query.Set("limit", strconv.Itoa(limit))
	if after != "" {
		query.Set("after", after)
	}
	if status != "" {
		query.Set("status", status)
	}
	if key == "messages show" || key == "messages events" || key == "domains show" || key == "domains check" || key == "uncertain resolve" {
		u, err := uuid.Parse(id)
		if err != nil || u == uuid.Nil || u.String() != id {
			return command{}, fmt.Errorf("--id must be a canonical UUID")
		}
	}
	base := internalmail.OperationsPath
	switch key {
	case "messages list":
		return command{http.MethodGet, base + "/messages?" + query.Encode(), nil}, nil
	case "messages show":
		return command{http.MethodGet, base + "/messages/" + id, nil}, nil
	case "messages events":
		return command{http.MethodGet, base + "/messages/" + id + "/events?" + query.Encode(), nil}, nil
	case "domains list":
		return command{http.MethodGet, base + "/domains?" + query.Encode(), nil}, nil
	case "domains show":
		return command{http.MethodGet, base + "/domains/" + id, nil}, nil
	case "domains check":
		return command{http.MethodGet, base + "/domains/" + id + "/check", nil}, nil
	case "suppressions list":
		return command{http.MethodGet, base + "/suppressions?" + query.Encode(), nil}, nil
	case "audit list":
		return command{http.MethodGet, base + "/audit?" + query.Encode(), nil}, nil
	case "relay status":
		return command{http.MethodGet, internalmail.RelayOperationsPath, nil}, nil
	case "relay events":
		return command{http.MethodGet, internalmail.RelayOperationsPath + "/events?" + query.Encode(), nil}, nil
	case "uncertain resolve":
		if reconcile.Validate() != nil {
			return command{}, fmt.Errorf("audit fields and exactly one matching confirmation are required")
		}
		return command{http.MethodPost, base + "/recipients/" + id + "/reconcile", reconcile}, nil
	case "retention preview":
		return command{http.MethodPost, base + "/retention/preview", struct{}{}}, nil
	case "retention apply":
		if audit.Validate() != nil {
			return command{}, fmt.Errorf("valid --request-id, --actor, --reason and --evidence are required")
		}
		return command{http.MethodPost, base + "/retention/apply", audit}, nil
	}
	return command{}, fmt.Errorf("unknown command")
}
