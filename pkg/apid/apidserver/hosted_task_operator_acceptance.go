//go:build hosted_task_acceptance && linux && amd64

package apidserver

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"slices"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/apid/taskrehearsal"
	"github.com/onebox-faas/faas/pkg/db"
	pgruntime "github.com/onebox-faas/faas/pkg/rehearsalpg/runtime"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// RunHostedTaskOperatorRecovery wires the existing APID owners in a one-shot
// process. This build is for the native acceptance command only.
func RunHostedTaskOperatorRecovery(ctx context.Context, accountID, token, intentID, plan string, archives tasktoolrehearsal.FixtureArchives,
	driver func(context.Context, *state.PgStore) (*taskrehearsal.NativeRecovery, func(), error), log *slog.Logger) (tasktoolrehearsal.RecoveryReceipt, error) {
	var receipt tasktoolrehearsal.RecoveryReceipt
	if !toolpublication.ValidID(intentID) || !toolpublication.ValidID(plan) || !toolpublication.ValidID(archives.Web) ||
		!toolpublication.ValidID(archives.Worker) || token == "" || driver == nil {
		return receipt, state.ErrInvalidArgument
	}
	cfg, err := LoadConfig("/etc/faas/apid.toml")
	if err != nil || cfg.DBURL == "" || cfg.Role != role.RoleSingleBox {
		return receipt, errors.New("load dedicated single-box APID configuration")
	}
	if err := installCredentialPolicy(os.Getenv, log); err != nil {
		return receipt, errors.New("load enrolled credential lookup policy")
	}
	pool, err := db.OpenWithAppName(ctx, cfg.DBURL, "faas-hosted-task-acceptance")
	if err != nil {
		return receipt, errors.New("open enrolled APID database")
	}
	defer pool.Close()
	store := state.NewPgStore(pool)
	account, key, err := store.AuthenticateKey(ctx, api.HashAPIKey(token))
	if err != nil || account.ID != accountID || key.AccountID != accountID || key.AppID != "" ||
		!slices.Contains(key.Scopes, api.ScopeAdmin) || slices.Contains(key.Scopes, api.ScopeAgent) || key.Label == api.MCPLoginKeyLabel {
		return receipt, errors.New("authenticate the enrolled account's personal administrator")
	}

	s, err := hostedTaskAcceptanceServer(ctx, cfg, store, pgNotifier{pool: pool, log: log}, log)
	if err != nil {
		return receipt, err
	}
	executor := s.newPrivateTools()
	defer executor.Close()
	recovery, closeDriver, err := driver(ctx, store)
	if err != nil {
		return receipt, err
	}
	defer closeDriver()
	run, evidence, err := executor.RecoverHostedTaskRehearsal(ctx, account, intentID, key.ID, plan, recovery)
	if err != nil {
		log.Warn("hosted Task journey did not complete", "run_id", run.ID)
		return receipt, errors.New("execute native hosted Task recovery")
	}
	if run.ID == "" || run.PlanSHA256 != plan || evidence.TaskID == "" {
		return receipt, errors.New("the hosted Task rehearsal plan needs a fresh, unconsumed approval")
	}

	inspect, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	cleanup, err := s.taskRehearsalProvisioner().ObserveCleanup(inspect, account.ID, run.ID, hostedTaskSourceExists)
	if err != nil {
		log.Warn("hosted Task cleanup observation failed", "run_id", run.ID)
		return receipt, errors.New("observe disposed hosted Task resources")
	}
	return tasktoolrehearsal.NewRecoveryReceipt(run.ID, archives, evidence, cleanup)
}

func hostedTaskAcceptanceServer(ctx context.Context, cfg *Config, store *state.PgStore, notify Notifier, log *slog.Logger) (*server, error) {
	getenv := os.Getenv
	recipientPath := getenv("FAAS_FLEET_AGE_RECIPIENT_PATH")
	if recipientPath == "" {
		recipientPath = getenv("FAAS_HOST_AGE_RECIPIENT_PATH")
	}
	if recipientPath != "" {
		recipient, err := secretbox.LoadRecipient(recipientPath)
		if err != nil {
			return nil, errors.New("load enrolled fleet seal recipient")
		}
		setSecretRecipient = func() *age.X25519Recipient { return recipient }
	}
	if err := loadSealDomains(getenv, log); err != nil || runtimeSecretRecipient() == nil {
		return nil, errors.New("load enrolled account and runtime seal domains")
	}
	if _, err := toolConnectionRecipients(); err != nil {
		return nil, errors.New("load independent account seal identity")
	}
	hmacKey, err := loadHostHMACKey(getenv("FAAS_HOST_HMAC_KEY_PATH"))
	if err != nil {
		return nil, errors.New("load enrolled runtime secret fingerprint key")
	}
	hostHMACKey = func() []byte { return hmacKey }
	s := newServer(store, log, cfg.GetAppsDomain(getenv), notify).WithReleasePhaseEnabled(releasePhaseEnabledFromEnv(getenv))
	s.rehearsalNetworkEnabled = getenv("FAAS_REHEARSAL_NETWORK_CONTAINMENT") == "1"
	s.rehearsalSimulationEnabled = getenv("FAAS_REHEARSAL_ACTION_SIMULATION") == "1"
	s.rehearsalWorkersEnabled = getenv("FAAS_REHEARSAL_WORKERS") == "1"
	s.rehearsalCommandsEnabled = getenv("FAAS_REHEARSAL_COMMAND_EXECUTION") == "1"
	// The running APID owns expiry reconciliation. This process only
	// opens the fixture cluster it will use for the one approved run.
	if getenv("FAAS_REHEARSAL_POSTGRES_FIXTURES") != "1" {
		return nil, errors.New("dedicated rehearsal PostgreSQL is disabled")
	}
	backend, err := pgruntime.NewPostgres(ctx, getenv("FAAS_REHEARSAL_POSTGRES_ADMIN_URL"))
	if err != nil {
		return nil, errors.New("open dedicated rehearsal PostgreSQL fixture cluster")
	}
	s.rehearsalPostgresEnabled, s.rehearsalPostgres = true, pgruntime.NewService(store, backend)
	return s, nil
}

func hostedTaskSourceExists(ctx context.Context, buildID string) (bool, error) {
	_, err := os.Lstat(hostedTaskBuildSourcePath(buildID))
	if !errors.Is(err, os.ErrNotExist) {
		return err == nil, err
	}
	return apidsource.RehearsalSourceExists(ctx, buildID)
}
