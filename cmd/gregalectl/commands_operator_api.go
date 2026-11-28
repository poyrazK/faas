// commands_operator_api.go — provider operations that used to ship in the
// customer CLI (`gregale admin`, `gregale postgres diagnostics|usage-import|
// reconcile`, `gregale github-webhook-secret`).
//
// Every route here is operator-only on the server: requireAdminMutation
// (operator session + recent TOTP step-up, bearer keys refused),
// requireOperatorSessionRead, or an admin scope plus the FAAS_ADMIN_EMAILS
// allowlist. A customer API key can never satisfy them, so the commands
// belong in the operator binary and use the operator session.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// operatorStdin is swapped by tests.
var operatorStdin io.Reader = os.Stdin

// operatorAPIRequest sends one request with the operator session. A non-empty
// idemKey is sent as the Idempotency-Key; otherwise randomIdem mints one.
func operatorAPIRequest(label, method, path string, input, output any, idemKey string, randomIdem bool) int {
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl %s: %v\n", label, err)
		return 1
	}
	var headers http.Header
	if idemKey != "" {
		headers = http.Header{"Idempotency-Key": {idemKey}}
		randomIdem = false
	}
	if err := newOperatorHTTPClient(&sess).doJSONWithHeaders(context.Background(), method, path, input, output, randomIdem, nil, headers); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl %s: %v\n", label, err)
		return 3
	}
	return 0
}

func parseOperatorFlags(fs *flag.FlagSet, args []string) bool {
	fs.SetOutput(osStderr)
	if err := fs.Parse(args); err != nil {
		return false
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintf(osStderr, "gregalectl %s: positional arguments are not accepted\n", fs.Name())
		return false
	}
	return true
}

func validOperatorUUID(label, name, value string) (string, bool) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl %s: --%s must be a UUID\n", label, name)
		return "", false
	}
	return parsed.String(), true
}

func validOperatorNote(label, name, value string) (string, bool) {
	value = strings.TrimSpace(value)
	if n := len(value); n < 3 || n > 500 {
		_, _ = fmt.Fprintf(osStderr, "gregalectl %s: --%s is required (3..500 chars)\n", label, name)
		return "", false
	}
	return value, true
}

// stableOperatorKey derives an Idempotency-Key from the whole operator
// intent, so re-running the same command after a network failure is a retry
// rather than a second credit or refund.
func stableOperatorKey(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + hex.EncodeToString(sum[:16])
}

// --- billing credit | refund | consume-credits ------------------------------

func cmdBillingCredit(args []string) int {
	const label = "billing credit"
	fs := flag.NewFlagSet(label, flag.ContinueOnError)
	accountID := fs.String("account-id", "", "account id (uuid)")
	cents := fs.Int64("cents", 0, "credit amount in USD cents")
	reason := fs.String("reason", "", "reason text (3..500 chars)")
	ack := fs.Bool("yes", false, "acknowledge issuing a credit")
	if !parseOperatorFlags(fs, args) {
		return 2
	}
	id, ok := validOperatorUUID(label, "account-id", *accountID)
	if !ok {
		return 2
	}
	note, ok := validOperatorNote(label, "reason", *reason)
	if !ok {
		return 2
	}
	if *cents <= 0 || !*ack {
		_, _ = fmt.Fprintf(osStderr, "gregalectl %s: --cents must be a positive integer and --yes is required\n", label)
		return 2
	}
	key := stableOperatorKey("ctl-billing-credit-", id, strconv.FormatInt(*cents, 10), note)
	var response api.AccountCreditResponse
	path := "/v1/admin/accounts/" + url.PathEscape(id) + "/credits"
	if code := operatorAPIRequest(label, http.MethodPost, path, map[string]any{"cents": *cents, "reason": note}, &response, key, false); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	_, _ = fmt.Fprintf(osStdout, "credit=%s account_id=%s cents=%d remaining=%d created_at=%s\n",
		response.ID, response.AccountID, *cents, response.CentsRemaining, response.CreatedAt.UTC().Format(time.RFC3339))
	return 0
}

func cmdBillingRefund(args []string) int {
	const label = "billing refund"
	fs := flag.NewFlagSet(label, flag.ContinueOnError)
	accountID := fs.String("account-id", "", "account id (uuid)")
	invoiceID := fs.String("invoice-id", "", "local invoice id (uuid)")
	cents := fs.Int64("cents", 0, "refund amount in USD cents")
	reason := fs.String("reason", "", "reason text (3..500 chars)")
	idem := fs.String("idempotency-key", "", "provider idempotency key; set it to make two equal partial refunds distinct")
	ack := fs.Bool("yes", false, "acknowledge moving money back to the customer")
	if !parseOperatorFlags(fs, args) {
		return 2
	}
	account, ok := validOperatorUUID(label, "account-id", *accountID)
	if !ok {
		return 2
	}
	invoice, ok := validOperatorUUID(label, "invoice-id", *invoiceID)
	if !ok {
		return 2
	}
	note, ok := validOperatorNote(label, "reason", *reason)
	if !ok {
		return 2
	}
	if *cents <= 0 || !*ack {
		_, _ = fmt.Fprintf(osStderr, "gregalectl %s: --cents must be a positive integer and --yes is required\n", label)
		return 2
	}
	key := strings.TrimSpace(*idem)
	if key == "" {
		key = stableOperatorKey("ctl-billing-refund-", account, invoice, strconv.FormatInt(*cents, 10), note)
	}
	body := map[string]any{"invoice_id": invoice, "amount_cents": *cents, "reason": note}
	var response api.AdminRefundResponse
	path := "/v1/admin/accounts/" + url.PathEscape(account) + "/refunds"
	if code := operatorAPIRequest(label, http.MethodPost, path, body, &response, key, false); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	_, _ = fmt.Fprintf(osStdout, "refunded_cents=%d invoice_id=%s provider=%s refund=%s status=%s\n",
		response.AmountCents, response.InvoiceID, response.Provider, response.ProviderRefundID, response.Status)
	return 0
}

func cmdBillingConsumeCredits(args []string) int {
	const label = "billing consume-credits"
	fs := flag.NewFlagSet(label, flag.ContinueOnError)
	invoiceID := fs.String("invoice-id", "", "local invoice id (uuid)")
	idem := fs.String("idempotency-key", "", "Idempotency-Key (generated when empty)")
	ack := fs.Bool("yes", false, "acknowledge consuming the account's credits")
	if !parseOperatorFlags(fs, args) {
		return 2
	}
	invoice, ok := validOperatorUUID(label, "invoice-id", *invoiceID)
	if !ok {
		return 2
	}
	if !*ack {
		_, _ = fmt.Fprintf(osStderr, "gregalectl %s: --yes is required\n", label)
		return 2
	}
	var response api.ConsumeInvoiceResponse
	path := "/v1/invoices/" + url.PathEscape(invoice) + "/consume-credits"
	if code := operatorAPIRequest(label, http.MethodPost, path, map[string]any{}, &response, strings.TrimSpace(*idem), true); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	_, _ = fmt.Fprintf(osStdout, "invoice_id=%s consumed_cents=%d remaining_credits_cents=%d\n",
		response.InvoiceID, response.ConsumedCents, response.RemainingCreditsCents)
	return 0
}

// --- billing postgres diagnostics | usage-import | reconcile ----------------

func cmdOperatorBillingPostgres(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(osStderr, "usage: gregalectl billing postgres <diagnostics|usage-import|reconcile> ...")
		return 2
	}
	switch args[0] {
	case "diagnostics":
		return cmdBillingPostgresDiagnostics(args[1:])
	case "usage-import":
		return cmdBillingPostgresEvidence("usage-import", args[1:])
	case "reconcile":
		return cmdBillingPostgresEvidence("reconcile", args[1:])
	default:
		_, _ = fmt.Fprintf(osStderr, "gregalectl billing postgres: unknown subcommand %q\n", args[0])
		return 2
	}
}

func cmdBillingPostgresDiagnostics(args []string) int {
	const label = "billing postgres diagnostics"
	fs := flag.NewFlagSet(label, flag.ContinueOnError)
	accountID := fs.String("account-id", "", "account id (uuid)")
	after := fs.String("after", "", "resume after the database id returned as next_cursor")
	limit := fs.Int("limit", 50, "maximum databases in this page (1..100)")
	if !parseOperatorFlags(fs, args) {
		return 2
	}
	id, ok := validOperatorUUID(label, "account-id", *accountID)
	if !ok {
		return 2
	}
	query := url.Values{"limit": {strconv.Itoa(*limit)}}
	if value := strings.TrimSpace(*after); value != "" {
		if uuid.Validate(value) != nil {
			_, _ = fmt.Fprintf(osStderr, "gregalectl %s: --after must be a UUID\n", label)
			return 2
		}
		query.Set("after", value)
	}
	if *limit < 1 || *limit > 100 {
		_, _ = fmt.Fprintf(osStderr, "gregalectl %s: --limit must be 1..100\n", label)
		return 2
	}
	var page api.ManagedPostgresAccountingDiagnosticsResponse
	path := "/v1/admin/managed-postgres/accounting/" + url.PathEscape(id) + "?" + query.Encode()
	if code := operatorAPIRequest(label, http.MethodGet, path, nil, &page, "", false); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(page)
	}
	_, _ = fmt.Fprintf(osStdout, "managed postgres accounting: %s (policy enabled: %t)\n", page.AccountID, page.PolicyEnabled)
	for _, d := range page.Items {
		_, _ = fmt.Fprintf(osStdout, "%s  %s  %s  blocking=%t\n", d.DatabaseID, d.Name, d.State, d.Blocking)
		_, _ = fmt.Fprintf(osStdout, "  accounting_root: %s; identity_known: %t\n", d.AccountingDatabaseID, d.IdentityKnown)
		_, _ = fmt.Fprintf(osStdout, "  reasons: %s\n", strings.Join(d.Reasons, ", "))
		_, _ = fmt.Fprintf(osStdout, "  required: %s — %s\n", operatorTime(d.RequiredFrom), operatorTime(d.RequiredUntil))
		_, _ = fmt.Fprintf(osStdout, "  collected: %s — %s; observed: %s\n", operatorTime(d.CollectedFrom), operatorTime(d.CollectedUntil), operatorTime(d.ObservedAt))
		if d.CorrectionRequiredAt != nil {
			_, _ = fmt.Fprintf(osStdout, "  final_correction: %s (required: %s)\n", operatorTime(d.CorrectionObservedAt), operatorTime(d.CorrectionRequiredAt))
		}
		if d.LeaseUntil != nil {
			_, _ = fmt.Fprintf(osStdout, "  lease_until: %s\n", operatorTime(d.LeaseUntil))
		}
	}
	if page.NextCursor != "" {
		_, _ = fmt.Fprintf(osStdout, "next_cursor=%s\n", page.NextCursor)
	}
	return 0
}

func operatorTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return "unknown"
	}
	return value.UTC().Format(time.RFC3339)
}

// cmdBillingPostgresEvidence previews (default) or applies (--apply) a
// retained-usage import (ADR-583) or a legacy accounting reconciliation
// (ADR-591). Apply needs the expected_revision returned by the preview.
func cmdBillingPostgresEvidence(action string, args []string) int {
	label := "billing postgres " + action
	fs := flag.NewFlagSet(label, flag.ContinueOnError)
	accountID := fs.String("account-id", "", "account id (uuid)")
	file := fs.String("file", "", "evidence JSON file (required)")
	apply := fs.Bool("apply", false, "apply the reviewed preview revision")
	if !parseOperatorFlags(fs, args) {
		return 2
	}
	id, ok := validOperatorUUID(label, "account-id", *accountID)
	if !ok {
		return 2
	}
	if strings.TrimSpace(*file) == "" {
		_, _ = fmt.Fprintf(osStderr, "gregalectl %s: --file is required\n", label)
		return 2
	}
	base := "/v1/admin/managed-postgres/accounting/" + url.PathEscape(id)
	if action == "usage-import" {
		request, err := readOperatorEvidence[api.ManagedPostgresUsageImportRequest](*file, 1<<20)
		if !checkEvidenceRevision(label, request.ExpectedRevision, *apply, err) {
			return 2
		}
		path := base + "/usage-imports"
		if !*apply {
			path += "/preview"
		}
		var result api.ManagedPostgresUsageImportResult
		if code := operatorAPIRequest(label, http.MethodPost, path, request, &result, "", *apply); code != 0 {
			return code
		}
		if jsonEnabled() {
			return emitOperatorJSON(result)
		}
		_, _ = fmt.Fprintf(osStdout, "import=%s applied=%t database=%s windows=%d previous_cost_millicents=%d imported_cost_millicents=%d cost_delta_millicents=%d collected_until=%s observed_at=%s revision=%s\n",
			result.ImportID, result.Applied, result.DatabaseID, result.WindowCount, result.PreviousCostMillicents,
			result.ImportedCostMillicents, result.CostDeltaMillicents, result.CollectedUntil.UTC().Format(time.RFC3339),
			result.ObservedAt.UTC().Format(time.RFC3339), result.Revision)
		return 0
	}
	request, err := readOperatorEvidence[api.ManagedPostgresAccountingReconciliationRequest](*file, 32<<10)
	if !checkEvidenceRevision(label, request.ExpectedRevision, *apply, err) {
		return 2
	}
	path := base + "/reconciliations"
	if !*apply {
		path += "/preview"
	}
	var result api.ManagedPostgresAccountingReconciliationResult
	if code := operatorAPIRequest(label, http.MethodPost, path, request, &result, "", *apply); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(result)
	}
	_, _ = fmt.Fprintf(osStdout, "reconciliation=%s applied=%t database=%s shutdown=%s observed_at=%s shared_accounting=%t usage_recovery_required=%t revision=%s\n",
		result.ReconciliationID, result.Applied, result.DatabaseID, result.ShutdownAt.UTC().Format(time.RFC3339),
		result.ObservedAt.UTC().Format(time.RFC3339), result.SharedAccounting, result.RequiresUsageRecovery, result.Revision)
	return 0
}

func checkEvidenceRevision(label, revision string, apply bool, readErr error) bool {
	switch {
	case readErr != nil:
		_, _ = fmt.Fprintf(osStderr, "gregalectl %s: invalid evidence file: %v\n", label, readErr)
		return false
	case apply && revision == "":
		_, _ = fmt.Fprintf(osStderr, "gregalectl %s: --apply needs expected_revision set to the revision the preview returned\n", label)
		return false
	case !apply && revision != "":
		_, _ = fmt.Fprintf(osStderr, "gregalectl %s: remove expected_revision before previewing\n", label)
		return false
	}
	return true
}

func readOperatorEvidence[T any](path string, limit int64) (T, error) {
	var request T
	if info, err := os.Lstat(path); err != nil {
		return request, err
	} else if !info.Mode().IsRegular() {
		return request, errors.New("evidence must be a regular file, not a symlink or device")
	}
	file, err := os.Open(path) //nolint:forbidigo // operator-supplied evidence file; regular-file check above and a bounded read follow.
	if err != nil {
		return request, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return request, err
	}
	if int64(len(data)) > limit {
		return request, fmt.Errorf("evidence exceeds %d bytes", limit)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return request, errors.New("expected exactly one JSON object")
	}
	return request, nil
}

// --- accounts abuse-hold | egress-flows -------------------------------------

func cmdAccountsAbuseHold(args []string) int {
	if len(args) == 0 || (args[0] != "place" && args[0] != "release") {
		_, _ = fmt.Fprintln(osStderr, "usage: gregalectl accounts abuse-hold <place|release> --account-id ID --note TEXT --yes")
		return 2
	}
	label := "accounts abuse-hold " + args[0]
	fs := flag.NewFlagSet(label, flag.ContinueOnError)
	accountID := fs.String("account-id", "", "account id (uuid)")
	note := fs.String("note", "", "audit note (3..500 chars)")
	ack := fs.Bool("yes", false, "acknowledge the hold change")
	if !parseOperatorFlags(fs, args[1:]) {
		return 2
	}
	id, ok := validOperatorUUID(label, "account-id", *accountID)
	if !ok {
		return 2
	}
	text, ok := validOperatorNote(label, "note", *note)
	if !ok {
		return 2
	}
	if !*ack {
		_, _ = fmt.Fprintf(osStderr, "gregalectl %s: --yes is required\n", label)
		return 2
	}
	method := http.MethodDelete
	if args[0] == "place" {
		method = http.MethodPost
	}
	var response api.AccountAbuseHoldActionResponse
	path := "/v1/admin/accounts/" + url.PathEscape(id) + "/abuse-hold"
	if code := operatorAPIRequest(label, method, path, api.AccountAbuseHoldAction{Note: text}, &response, "", true); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	held, reason := response.AbuseHold != nil, ""
	if held {
		reason = response.AbuseHold.Reason
	}
	_, _ = fmt.Fprintf(osStdout, "account_id=%s held=%t changed=%t reason=%s\n", response.AccountID, held, response.Changed, reason)
	return 0
}

func cmdAccountsEgressFlows(args []string) int {
	const label = "accounts egress-flows"
	fs := flag.NewFlagSet(label, flag.ContinueOnError)
	remote := fs.String("remote", "", "remote IP address or CIDR")
	accountID := fs.String("account-id", "", "account id (uuid)")
	verdict := fs.String("verdict", "", "allowed or blocked (default: both)")
	from := fs.String("from", "", "window start, RFC 3339 (default: 24h before --to)")
	to := fs.String("to", "", "window end, RFC 3339 (default: now)")
	limit := fs.Int("limit", 0, "maximum rows (default 200, max 1000)")
	if !parseOperatorFlags(fs, args) {
		return 2
	}
	query := url.Values{}
	if value := strings.TrimSpace(*remote); value != "" {
		query.Set("remote", value)
	}
	if value := strings.TrimSpace(*accountID); value != "" {
		id, ok := validOperatorUUID(label, "account-id", value)
		if !ok {
			return 2
		}
		query.Set("account_id", id)
	}
	switch value := strings.TrimSpace(*verdict); value {
	case "":
	case "allowed", "blocked":
		query.Set("verdict", value)
	default:
		_, _ = fmt.Fprintf(osStderr, "gregalectl %s: --verdict must be allowed or blocked\n", label)
		return 2
	}
	for name, value := range map[string]string{"from": *from, "to": *to} {
		if value == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			_, _ = fmt.Fprintf(osStderr, "gregalectl %s: --%s must be an RFC 3339 time\n", label, name)
			return 2
		}
		query.Set(name, parsed.UTC().Format(time.RFC3339))
	}
	if *limit < 0 || *limit > 1000 {
		_, _ = fmt.Fprintf(osStderr, "gregalectl %s: --limit must be 1..1000\n", label)
		return 2
	}
	if *limit > 0 {
		query.Set("limit", strconv.Itoa(*limit))
	}
	path := "/v1/admin/egress-flows"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var response api.EgressFlowLogResponse
	if code := operatorAPIRequest(label, http.MethodGet, path, nil, &response, "", false); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	for _, f := range response.Flows {
		verdict, protocol := "allowed", f.Protocol
		if f.Blocked {
			verdict = "blocked"
		}
		if protocol == "" {
			protocol = "tcp"
		}
		_, _ = fmt.Fprintf(osStdout, "observed_at=%s verdict=%s node=%s account_id=%s app_id=%s instance_id=%s remote=%s %s\n",
			f.ObservedAt.UTC().Format(time.RFC3339), verdict, f.Node, f.AccountID, f.AppID, f.InstanceID, protocol,
			net.JoinHostPort(f.RemoteIP, strconv.Itoa(f.RemotePort)))
	}
	if response.Truncated {
		_, _ = fmt.Fprintln(osStdout, "truncated=true (narrow the window or filter to see more)")
	}
	return 0
}

// --- github webhook-secret set ----------------------------------------------

// cmdGithubWebhookSecret rotates a legacy installation-scoped webhook secret
// (ADR-012 §7). The secret is read from stdin so it never lands in argv or
// shell history.
func cmdGithubWebhookSecret(args []string) int {
	const label = "github webhook-secret set"
	if len(args) == 0 || args[0] != "set" {
		_, _ = fmt.Fprintln(osStderr, "usage: gregalectl github webhook-secret set --installation-id ID --yes < secret.hex")
		return 2
	}
	fs := flag.NewFlagSet(label, flag.ContinueOnError)
	installationID := fs.Int64("installation-id", 0, "GitHub App installation_id")
	ack := fs.Bool("yes", false, "acknowledge replacing the installation's webhook secret")
	if !parseOperatorFlags(fs, args[1:]) {
		return 2
	}
	if *installationID <= 0 || !*ack {
		_, _ = fmt.Fprintf(osStderr, "gregalectl %s: --installation-id and --yes are required\n", label)
		return 2
	}
	secret := ""
	scanner := bufio.NewScanner(operatorStdin)
	scanner.Buffer(make([]byte, 0, 256), 256)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" && !strings.HasPrefix(line, "#") {
			secret = line
			break
		}
	}
	if err := scanner.Err(); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl %s: read stdin: %v\n", label, err)
		return 2
	}
	raw, err := hex.DecodeString(secret)
	if err != nil || len(raw) < 16 || len(raw) > 64 {
		_, _ = fmt.Fprintf(osStderr, "gregalectl %s: stdin must hold one 16-64 byte secret as hex (32-128 hex chars)\n", label)
		return 2
	}
	request := api.AdminSetGithubWebhookSecretRequest{InstallationID: *installationID, SecretHex: hex.EncodeToString(raw)}
	var response api.AdminSetGithubWebhookSecretResponse
	if code := operatorAPIRequest(label, http.MethodPost, "/v1/admin/github-webhook-secrets", request, &response, "", true); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	_, _ = fmt.Fprintf(osStdout, "installation_id=%d upgraded_at=%s upgraded_by=%s\n",
		*installationID, response.UpgradedAt.UTC().Format(time.RFC3339), response.UpgradedBy)
	return 0
}

// --- secrets rekey-progress -------------------------------------------------

// cmdSecretsRekeyProgress reads the ADR-089 secret re-key walk's counters.
func cmdSecretsRekeyProgress(args []string) int {
	const label = "secrets rekey-progress"
	fs := flag.NewFlagSet(label, flag.ContinueOnError)
	if !parseOperatorFlags(fs, args) {
		return 2
	}
	var progress api.RekeyProgress
	if code := operatorAPIRequest(label, http.MethodGet, "/v1/admin/secrets/rekey-progress", nil, &progress, "", false); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(progress)
	}
	_, _ = fmt.Fprintf(osStdout, "total=%d rekeyed=%d skipped=%d failed=%d last_id=%s\n",
		progress.Total, progress.Rekeyed, progress.Skipped, progress.Failed, progress.LastID)
	return 0
}
