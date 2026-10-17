package mailoutbox

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

const operatorUsage = `Usage: apid mail-outbox <command> [flags]
  reconcile --id INTENT_UUID --message-id MAILD_MESSAGE_UUID --expected-revision N
            --recovery-id UUID --request-id UUID --actor NAME --reason SLUG --evidence REF
  audit     --request-id UUID

Local operator mode: requires an explicit DATABASE_URL (or FAAS_DATABASE_URL)
for apid's owner database and matching FAAS_INTERNAL_MAIL_NAMESPACE (default platform).
Master, operations, producer-outbox and outbox-reconciliation flags must be exact 1.
Verification intents also require FAAS_INTERNAL_MAIL_VERIFICATION_OUTBOX_ENABLED=1.
Password-reset intents also require FAAS_INTERNAL_MAIL_PASSWORD_RESET_OUTBOX_ENABLED=1.
Magic-link intents also require FAAS_INTERNAL_MAIL_MAGIC_LINK_OUTBOX_ENABLED=1.
MFA disable requests also require FAAS_INTERNAL_MAIL_MFA_DISABLE_OUTBOX_ENABLED=1.
Deployment-failure notices also require FAAS_INTERNAL_MAIL_DEPLOYMENT_FAILED_OUTBOX_ENABLED=1.
Subscription-ended confirmations also require FAAS_INTERNAL_MAIL_SUBSCRIPTION_ENDED_OUTBOX_ENABLED=1.
Account suspension notices also require FAAS_INTERNAL_MAIL_ACCOUNT_SUSPENDED_OUTBOX_ENABLED=1.
Account restoration confirmations also require FAAS_INTERNAL_MAIL_ACCOUNT_RESTORED_OUTBOX_ENABLED=1.
Payment failure warnings also require FAAS_INTERNAL_MAIL_PAYMENT_FAILED_OUTBOX_ENABLED=1.
MFA enabled alerts also require FAAS_INTERNAL_MAIL_MFA_ENABLED_ALERT_OUTBOX_ENABLED=1.
Password changed alerts also require FAAS_INTERNAL_MAIL_PASSWORD_CHANGED_ALERT_OUTBOX_ENABLED=1.
MFA disabled alerts also require FAAS_INTERNAL_MAIL_MFA_DISABLED_ALERT_OUTBOX_ENABLED=1.
MFA recovery alerts also require FAAS_INTERNAL_MAIL_MFA_RECOVERY_ALERT_OUTBOX_ENABLED=1.
First inspect mailctl outbox admission --id UUID on maild's private operator socket.
Save the request UUID before writing; reuse identical arguments after a lost response.
This command opens no listener and never admits or resends mail.
`

type operatorCommand struct {
	audit bool
	id    string
	input ReconciliationRequest
}

func parseOperatorCommand(args []string, diagnostics io.Writer) (operatorCommand, error) {
	var cmd operatorCommand
	fs := flag.NewFlagSet("apid mail-outbox "+args[0], flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	fs.StringVar(&cmd.input.RequestID, "request-id", "", "saved operation UUID")
	switch args[0] {
	case "audit":
		cmd.audit = true
	case "reconcile":
		fs.StringVar(&cmd.id, "id", "", "producer intent UUID")
		fs.StringVar(&cmd.input.MessageID, "message-id", "", "confirmed maild message UUID")
		fs.Int64Var(&cmd.input.ExpectedRevision, "expected-revision", 0, "current intent revision")
		fs.StringVar(&cmd.input.RecoveryID, "recovery-id", "", "current recovery UUID; zero before recovery")
		fs.StringVar(&cmd.input.Actor, "actor", "", "operator assertion")
		fs.StringVar(&cmd.input.Reason, "reason", "", "reason slug")
		fs.StringVar(&cmd.input.Evidence, "evidence", "", "evidence reference, never message contents")
	default:
		return cmd, internalmail.ErrInvalid
	}
	if err := fs.Parse(args[1:]); err != nil {
		return cmd, internalmail.ErrInvalid
	}
	if fs.NArg() != 0 || !validID(cmd.input.RequestID) || !cmd.audit && (!validID(cmd.id) || cmd.input.Validate() != nil) {
		return cmd, internalmail.ErrInvalid
	}
	return cmd, nil
}

// RunOperator is dispatched only by the apid executable before daemon startup.
// With the flag off it reads no database configuration, credentials or state.
func RunOperator(ctx context.Context, args []string, getenv func(string) string, out, diagnostics io.Writer) int {
	if len(args) == 0 || len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		_, _ = io.WriteString(out, operatorUsage)
		return 0
	}
	enabled := func() bool {
		return internalmail.OutboxReconciliationEnabled(getenv) && internalmail.OperationsEnabled(getenv)
	}
	if !enabled() {
		_, _ = fmt.Fprintln(diagnostics, "apid mail-outbox: reconciliation disabled")
		return 1
	}
	cmd, err := parseOperatorCommand(args, diagnostics)
	if err != nil {
		_, _ = fmt.Fprintln(diagnostics, "apid mail-outbox: invalid command; use help")
		return 2
	}
	namespace := getenv("FAAS_INTERNAL_MAIL_NAMESPACE")
	if namespace == "" {
		namespace = "platform"
	}
	dsn := getenv("DATABASE_URL")
	if dsn == "" {
		dsn = getenv("FAAS_DATABASE_URL")
	}
	if dsn == "" || !regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`).MatchString(namespace) {
		_, _ = fmt.Fprintln(diagnostics, "apid mail-outbox: explicit owner database and valid namespace required")
		return 1
	}
	pass, cancel := context.WithTimeout(ctx, api.InternalMailAPITimeout)
	defer cancel()
	pool, err := operatorPool(pass, dsn)
	if err != nil {
		_, _ = fmt.Fprintln(diagnostics, "apid mail-outbox: owner database unavailable")
		return 1
	}
	defer pool.Close()
	reconciler, err := NewReconciler(pool, namespace, enabled)
	if err != nil {
		return writeOperatorError(diagnostics, err)
	}
	reconciler = reconciler.WithVerification(func() bool { return internalmail.VerificationOutboxEnabled(getenv) })
	reconciler = reconciler.WithPasswordReset(func() bool { return internalmail.PasswordResetOutboxEnabled(getenv) })
	reconciler = reconciler.WithMagicLink(func() bool { return internalmail.MagicLinkOutboxEnabled(getenv) })
	reconciler = reconciler.WithMFADisable(func() bool { return internalmail.MFADisableOutboxEnabled(getenv) })
	reconciler = reconciler.WithDeploymentFailed(func() bool { return internalmail.DeploymentFailedOutboxEnabled(getenv) })
	reconciler = reconciler.WithSubscriptionEnded(func() bool { return internalmail.SubscriptionEndedOutboxEnabled(getenv) })
	reconciler = reconciler.WithQuotaWarning(func() bool { return internalmail.QuotaWarningOutboxEnabled(getenv) })
	reconciler = reconciler.WithNonpaymentDeletion(func() bool { return internalmail.NonpaymentDeletionOutboxEnabled(getenv) })
	reconciler = reconciler.WithAccountSuspended(func() bool { return internalmail.AccountSuspendedOutboxEnabled(getenv) })
	reconciler = reconciler.WithAccountRestored(func() bool { return internalmail.AccountRestoredOutboxEnabled(getenv) })
	reconciler = reconciler.WithPaymentFailed(func() bool { return internalmail.PaymentFailedOutboxEnabled(getenv) })
	reconciler = reconciler.WithMFAEnabledAlert(func() bool { return internalmail.MFAEnabledAlertOutboxEnabled(getenv) })
	reconciler = reconciler.WithPasswordChangedAlert(func() bool { return internalmail.PasswordChangedAlertOutboxEnabled(getenv) })
	reconciler = reconciler.WithMFADisabledAlert(func() bool { return internalmail.MFADisabledAlertOutboxEnabled(getenv) })
	reconciler = reconciler.WithMFARecoveryAlert(func() bool { return internalmail.MFARecoveryAlertOutboxEnabled(getenv) })
	var record ReconciliationRecord
	if cmd.audit {
		record, err = reconciler.Audit(pass, cmd.input.RequestID)
	} else {
		record, err = reconciler.Acknowledge(pass, cmd.id, cmd.input)
	}
	if err != nil {
		return writeOperatorError(diagnostics, err)
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(record); err != nil {
		return 1
	}
	return 0
}

func operatorPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = api.InternalMailOutboxOperatorMaxConnections
	cfg.MinConns, cfg.MinIdleConns = 0, 0
	cfg.ConnConfig.ConnectTimeout = api.InternalMailAPITimeout
	cfg.ConnConfig.RuntimeParams["application_name"] = "faas-apid-mail-outbox"
	return pgxpool.NewWithConfig(ctx, cfg)
}

func writeOperatorError(out io.Writer, err error) int {
	message := "operation incomplete; inspect audit with the saved request ID before retrying"
	switch {
	case errors.Is(err, internalmail.ErrDisabled):
		message = "reconciliation disabled"
	case errors.Is(err, internalmail.ErrInvalid):
		message = "invalid request"
	case errors.Is(err, internalmail.ErrNotFound):
		message = "intent or audit not found in this namespace"
	case errors.Is(err, internalmail.ErrConflict):
		message = "reconciliation conflict; inspect current admission, revision and recovery state"
	}
	_, _ = fmt.Fprintln(out, "apid mail-outbox:", message)
	return 1
}
