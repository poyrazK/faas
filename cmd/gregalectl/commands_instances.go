// commands_instances.go — authenticated operator instance recovery.
//
// Normal operation submits an MFA-gated apid request and polls the durable
// operator_intents receipt written for schedd. Direct state.Store/schedd
// access remains available only through --break-glass-local --yes --reason,
// using the routing and mTLS material installed for meterd.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

const (
	dispatchInstances              = "instances"
	instanceMutationDefaultTimeout = 30 * time.Second
	operatorTraceIDHeader          = "X-Trace-Id"
)

type instanceMutationSpec struct {
	action     string
	target     string
	reason     string
	traceID    string
	requestKey string
	timeout    time.Duration
}

func cmdInstancesDispatch(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl instances: missing subcommand; want force-park|force-cold-boot|force-restart|wait")
		return 2
	}
	switch args[0] {
	case "wait":
		return cmdInstancesWait(args[1:])
	case "force-park":
		return cmdInstancesForcePark(args[1:])
	case "force-cold-boot":
		return cmdInstancesForceColdBoot(args[1:])
	case "force-restart":
		return cmdInstancesForceRestart(args[1:])
	default:
		_, _ = fmt.Fprintf(osStderr, "gregalectl instances: unknown subcommand %q\n", args[0])
		return 2
	}
}

const operatorMeterdConfigPath = "/etc/faas/meterd.toml"

// operatorScheddConfig is the subset of meterd.toml gregalectl needs to
// reach schedd. Reusing the installed meterd client leaf keeps the local,
// root-only operator path on the same mTLS trust boundary as meterd without
// duplicating routing configuration in a second file.
type operatorScheddConfig struct {
	Target   string `toml:"schedd_socket"`
	CertPath string `toml:"schedd_tls_cert_path"`
	KeyPath  string `toml:"schedd_tls_key_path"`
	CAPath   string `toml:"schedd_tls_ca_path"`
}

func loadOperatorScheddConfig(path string) (operatorScheddConfig, error) {
	cfg := operatorScheddConfig{Target: "unix:///run/faas/schedd.sock"}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return operatorScheddConfig{}, fmt.Errorf("read schedd routing config %q: %w", path, err)
	}
	if _, err := toml.Decode(string(b), &cfg); err != nil {
		return operatorScheddConfig{}, fmt.Errorf("parse schedd routing config %q: %w", path, err)
	}
	if cfg.Target == "" {
		cfg.Target = "unix:///run/faas/schedd.sock"
	}
	return cfg, nil
}

func resolveOperatorScheddConnection(path string) (string, *tls.Config, error) {
	cfg, err := loadOperatorScheddConfig(path)
	if err != nil {
		return "", nil, err
	}
	target := cfg.Target
	if override := os.Getenv("FAAS_SCHEDD_ADDR"); override != "" {
		target = override
	}
	tlsCfg, err := wire.LoadClientTLSConfigWithPrefix("schedd_", cfg.CertPath, cfg.KeyPath, cfg.CAPath)
	if err != nil {
		return "", nil, fmt.Errorf("load schedd TLS: %w", err)
	}
	return target, tlsCfg, nil
}

// openScheddClientFromEnv is reserved for break glass and dials schedd using
// the routing and mTLS material installed for meterd. FAAS_SCHEDD_ADDR remains
// an explicit target override, but TLS still comes from meterd.toml.
func openScheddClientFromEnv() (*scheddgrpc.Client, func(), error) {
	target, tlsCfg, err := resolveOperatorScheddConnection(operatorMeterdConfigPath)
	if err != nil {
		return nil, func() {}, fmt.Errorf("gregalectl instances: %w", err)
	}
	cli, err := scheddgrpc.DialContext(context.Background(), target, tlsCfg)
	if err != nil {
		return nil, func() {}, fmt.Errorf("gregalectl instances: dial schedd %s: %w", target, err)
	}
	return cli, func() { _ = cli.Close() }, nil
}

func cmdInstancesForcePark(args []string) int {
	fs := flag.NewFlagSet("force-park", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	instanceID := fs.String("instance-id", "", "instance id (uuid) to force-park")
	reason := fs.String("reason", "", "audit reason slug ([a-z0-9_]{1,64})")
	timeout := fs.Duration("timeout", instanceMutationDefaultTimeout, "maximum durable-intent wait")
	requestKey := fs.String("request-key", "", "reuse this key with the same target and reason after an uncertain submission")
	traceIDFlag := fs.String("trace-id", "", "OTel 32-char-hex trace id (auto-generated when empty)")
	breakGlass := fs.Bool("break-glass-local", false, "bypass apid and call the local schedd directly")
	ack := fs.Bool("yes", false, "acknowledge that the instance will be evicted from the wake path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *instanceID == "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl instances force-park: --instance-id required")
		return 2
	}
	if !*ack {
		_, _ = fmt.Fprintln(osStderr, "gregalectl instances force-park: --yes required (the instance will be evicted from the wake path)")
		return 2
	}
	traceID := instanceTraceID(*traceIDFlag)
	if *breakGlass {
		if *requestKey != "" {
			_, _ = fmt.Fprintln(osStderr, "--request-key requires the apid submission path")
			return 2
		}
		if !instanceBreakGlassAllowed("force-park", *reason) {
			return 2
		}
		return forceParkBreakGlass(*instanceID, strings.TrimSpace(*reason), traceID)
	}
	return mutateInstanceViaAPI(instanceMutationSpec{
		action: "force-park", target: *instanceID,
		reason:  instanceMutationReason(*reason, "operator_force_park"),
		traceID: traceID, requestKey: *requestKey, timeout: *timeout,
	})
}

func cmdInstancesForceColdBoot(args []string) int {
	fs := flag.NewFlagSet("force-cold-boot", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	appSlug := fs.String("app-slug", "", "app slug whose latest deployment will be cold-booted on next wake")
	reason := fs.String("reason", "", "audit reason slug ([a-z0-9_]{1,64})")
	timeout := fs.Duration("timeout", instanceMutationDefaultTimeout, "maximum durable-intent wait")
	requestKey := fs.String("request-key", "", "reuse this key with the same target and reason after an uncertain submission")
	traceIDFlag := fs.String("trace-id", "", "OTel 32-char-hex trace id (auto-generated when empty)")
	breakGlass := fs.Bool("break-glass-local", false, "bypass apid and use the local database and schedd")
	ack := fs.Bool("yes", false, "acknowledge that the customer's next wake will be a cold boot")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *appSlug == "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl instances force-cold-boot: --app-slug required")
		return 2
	}
	if !*ack {
		_, _ = fmt.Fprintln(osStderr, "gregalectl instances force-cold-boot: --yes required (the customer's next wake will be a cold boot)")
		return 2
	}
	traceID := instanceTraceID(*traceIDFlag)
	if *breakGlass {
		if *requestKey != "" {
			_, _ = fmt.Fprintln(osStderr, "--request-key requires the apid submission path")
			return 2
		}
		if !instanceBreakGlassAllowed("force-cold-boot", *reason) {
			return 2
		}
		return forceColdBootBreakGlass(*appSlug, strings.TrimSpace(*reason), traceID)
	}
	return mutateInstanceViaAPI(instanceMutationSpec{
		action: "force-cold-boot", target: *appSlug,
		reason:  instanceMutationReason(*reason, "operator_force_cold_boot"),
		traceID: traceID, requestKey: *requestKey, timeout: *timeout,
	})
}

func cmdInstancesForceRestart(args []string) int {
	fs := flag.NewFlagSet("force-restart", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	instanceID := fs.String("instance-id", "", "instance id (uuid) to force-restart (kill + cold-boot on next wake)")
	reason := fs.String("reason", "", "audit reason slug ([a-z0-9_]{1,64})")
	timeout := fs.Duration("timeout", instanceMutationDefaultTimeout, "maximum durable-intent wait")
	requestKey := fs.String("request-key", "", "reuse this key with the same target and reason after an uncertain submission")
	traceIDFlag := fs.String("trace-id", "", "OTel 32-char-hex trace id (auto-generated when empty)")
	breakGlass := fs.Bool("break-glass-local", false, "bypass apid and call the local schedd directly")
	ack := fs.Bool("yes", false, "acknowledge that the instance will be killed and the next wake will be a cold boot")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *instanceID == "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl instances force-restart: --instance-id required")
		return 2
	}
	if !*ack {
		_, _ = fmt.Fprintln(osStderr, "gregalectl instances force-restart: --yes required (the instance will be killed and the next wake will be a cold boot)")
		return 2
	}
	traceID := instanceTraceID(*traceIDFlag)
	if *breakGlass {
		if *requestKey != "" {
			_, _ = fmt.Fprintln(osStderr, "--request-key requires the apid submission path")
			return 2
		}
		if !instanceBreakGlassAllowed("force-restart", *reason) {
			return 2
		}
		return forceRestartBreakGlass(*instanceID, strings.TrimSpace(*reason), traceID)
	}
	return mutateInstanceViaAPI(instanceMutationSpec{
		action: "force-restart", target: *instanceID,
		reason:  instanceMutationReason(*reason, "operator_force_restart"),
		traceID: traceID, requestKey: *requestKey, timeout: *timeout,
	})
}

func instanceTraceID(raw string) string {
	if strings.TrimSpace(raw) != "" {
		return strings.TrimSpace(raw)
	}
	return wire.NewTraceID()
}

// cmdInstancesWait only reads an existing intent; it never submits an action.
func cmdInstancesWait(args []string) int {
	fs := flag.NewFlagSet("instances wait", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	intentID := fs.String("intent-id", "", "existing operator intent UUID to poll")
	timeout := fs.Duration("timeout", instanceMutationDefaultTimeout, "maximum wait")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	id, err := uuid.Parse(*intentID)
	if err != nil || id == uuid.Nil || *timeout <= 0 || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl instances wait: a valid --intent-id and positive --timeout are required")
		return 2
	}
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl instances wait:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	accepted := api.OperatorIntentAcceptedResponse{IntentID: id.String(), StatusURL: "/v1/admin/operator-intents/" + id.String()}
	return waitForInstanceIntent(ctx, newOperatorHTTPClient(&sess), instanceMutationSpec{action: "wait", timeout: *timeout}, accepted)
}

func instanceMutationReason(raw, fallback string) string {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	return strings.TrimSpace(raw)
}

func instanceBreakGlassAllowed(action, reason string) bool {
	if strings.TrimSpace(reason) == "" {
		_, _ = fmt.Fprintf(osStderr, "gregalectl instances %s: --break-glass-local requires --yes and an explicit --reason\n", action)
		return false
	}
	return true
}

func instanceMutationPath(spec instanceMutationSpec) (string, error) {
	var prefix string
	switch spec.action {
	case "force-park", "force-restart":
		prefix = "/v1/admin/instances/"
	case "force-cold-boot":
		prefix = "/v1/admin/apps/"
	default:
		return "", fmt.Errorf("unsupported instance action %q", spec.action)
	}
	return prefix + url.PathEscape(spec.target) + "/" + spec.action + "?confirm=true&reason=" + url.QueryEscape(spec.reason), nil
}

func mutateInstanceViaAPI(spec instanceMutationSpec) int {
	if spec.timeout <= 0 {
		_, _ = fmt.Fprintf(osStderr, "gregalectl instances %s: --timeout must be positive\n", spec.action)
		return 2
	}
	if spec.requestKey != "" && !state.ValidOperatorRecoveryKey(spec.requestKey) {
		_, _ = fmt.Fprintln(osStderr, "--request-key must contain 1 to 255 visible ASCII characters")
		return 2
	}
	path, err := instanceMutationPath(spec)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, err)
		return 2
	}
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl instances %s: %v\n", spec.action, err)
		return 1
	}
	client := newOperatorHTTPClient(&sess)
	ctx, cancel := context.WithTimeout(context.Background(), spec.timeout)
	defer cancel()

	if spec.requestKey == "" {
		spec.requestKey = uuid.NewString()
	}
	_, _ = fmt.Fprintf(osStderr, "gregalectl instances %s: request_key=%s (retry with --request-key and the same target and reason)\n", spec.action, spec.requestKey)
	headers := make(http.Header)
	headers.Set(operatorTraceIDHeader, spec.traceID)
	headers.Set("Idempotency-Key", spec.requestKey)
	var accepted api.OperatorIntentAcceptedResponse
	if err := client.doJSONWithHeaders(ctx, http.MethodPost, path, nil, &accepted, false, nil, headers); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl instances %s: %v\n", spec.action, err)
		return 1
	}
	if accepted.IntentID == "" || accepted.StatusURL == "" {
		_, _ = fmt.Fprintf(osStderr, "gregalectl instances %s: apid returned an incomplete intent receipt\n", spec.action)
		return 1
	}

	_, _ = fmt.Fprintf(osStderr, "intent=%s; resume: gregalectl instances wait --intent-id=%s\n", accepted.IntentID, accepted.IntentID)
	return waitForInstanceIntent(ctx, client, spec, accepted)
}

func waitForInstanceIntent(ctx context.Context, client *operatorHTTPClient, spec instanceMutationSpec, accepted api.OperatorIntentAcceptedResponse) int {
	var outcome api.OperatorIntentResponse
	for {
		if err := client.doJSON(ctx, http.MethodGet, accepted.StatusURL, nil, &outcome, false, nil); err != nil {
			_, _ = fmt.Fprintf(osStderr, "gregalectl instances %s: poll intent %s: %v\n", spec.action, accepted.IntentID, err)
			return 1
		}
		switch state.OperatorIntentStatus(outcome.Status) {
		case state.OperatorIntentSucceeded:
			return renderInstanceMutationOutcome(spec, accepted, outcome)
		case state.OperatorIntentOutcomeUnknown:
			if jsonEnabled() {
				if code := renderInstanceMutationOutcome(spec, accepted, outcome); code != 0 {
					return code
				}
			} else {
				_, _ = fmt.Fprintf(osStderr, "gregalectl instances %s: intent %s outcome unknown: %s; automatic redispatch stopped; inspect %s before another action\n", spec.action, accepted.IntentID, outcome.Error, accepted.StatusURL)
			}
			return 1
		case state.OperatorIntentFailed, state.OperatorIntentCancelled:
			_, _ = fmt.Fprintf(osStderr, "gregalectl instances %s: intent %s %s: %s\n", spec.action, accepted.IntentID, outcome.Status, outcome.Error)
			return 1
		}
		select {
		case <-ctx.Done():
			_, _ = fmt.Fprintf(osStderr, "gregalectl instances %s: intent %s did not finish before %s\n", spec.action, accepted.IntentID, spec.timeout)
			return 1
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func renderInstanceMutationOutcome(spec instanceMutationSpec, accepted api.OperatorIntentAcceptedResponse, outcome api.OperatorIntentResponse) int {
	if spec.action == "wait" {
		if jsonEnabled() {
			return emitOperatorJSON(outcome)
		}
		_, _ = fmt.Fprintf(osStdout, "intent=%s kind=%s status=%s target=%s trace_id=%s\n", outcome.IntentID, outcome.Kind, outcome.Status, outcome.TargetID, outcome.TraceID)
		return 0
	}
	if jsonEnabled() {
		return emitOperatorJSON(struct {
			Operation  api.OperatorIntentAcceptedResponse `json:"operation"`
			Outcome    api.OperatorIntentResponse         `json:"outcome"`
			RequestKey string                             `json:"request_key"`
		}{accepted, outcome, spec.requestKey})
	}
	traceID := outcome.TraceID
	if traceID == "" {
		traceID = spec.traceID
	}
	_, _ = fmt.Fprintf(osStdout, "%s succeeded target=%s intent=%s trace_id=%s", spec.action, spec.target, accepted.IntentID, traceID)
	if accepted.DeploymentID != "" {
		_, _ = fmt.Fprintf(osStdout, " deployment=%s", accepted.DeploymentID)
	}
	if park := outcome.ParkExecution; park != nil {
		_, _ = fmt.Fprintf(osStdout, " park_phase=%s terminal_state=%s capture_completed=%t cold_boot_required=%t", park.Phase, park.TerminalState, park.CaptureCompleted, park.ColdBootRequired)
	}
	if len(outcome.SnapIDsMarkedStale) > 0 {
		_, _ = fmt.Fprintf(osStdout, " snap_ids_marked_stale=%v", outcome.SnapIDsMarkedStale)
	}
	_, _ = fmt.Fprintln(osStdout)
	return 0
}

func forceParkBreakGlass(instanceID, reason, traceID string) int {
	_, _ = fmt.Fprintf(osStderr, "BREAK GLASS: bypassing apid audit; action=force-park target=%s reason=%s\n", instanceID, reason)
	cli, closeFn, err := openScheddClientFromEnv()
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, err)
		return 1
	}
	defer closeFn()
	if err := cli.ParkInstance(context.Background(), instanceID, reason, traceID); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl instances force-park:", err)
		return 1
	}
	_, _ = fmt.Fprintf(osStdout, "force-parked %s reason=%s trace_id=%s\n", instanceID, reason, traceID)
	return 0
}

func forceColdBootBreakGlass(appSlug, reason, traceID string) int {
	_, _ = fmt.Fprintf(osStderr, "BREAK GLASS: bypassing apid audit and reading Postgres directly; action=force-cold-boot target=%s reason=%s\n", appSlug, reason)
	st, closeFn, err := computeNodesStoreOpener()
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, err)
		return 1
	}
	defer closeFn()
	ctx := context.Background()
	app, err := st.AppBySlug(ctx, appSlug)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl instances force-cold-boot:", err)
		return 1
	}
	dep, err := st.LatestDeployment(ctx, app.ID)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl instances force-cold-boot:", err)
		return 1
	}
	cli, scheddClose, err := openScheddClientFromEnv()
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, err)
		return 1
	}
	defer scheddClose()
	snapIDs, err := cli.ForceColdBootNextWake(ctx, dep.ID, traceID)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl instances force-cold-boot:", err)
		return 1
	}
	_, _ = fmt.Fprintf(osStdout, "force-cold-boot app=%s deployment=%s reason=%s trace_id=%s snap_ids=%v\n", appSlug, dep.ID, reason, traceID, snapIDs)
	return 0
}

func forceRestartBreakGlass(instanceID, reason, traceID string) int {
	_, _ = fmt.Fprintf(osStderr, "BREAK GLASS: bypassing apid audit; action=force-restart target=%s reason=%s\n", instanceID, reason)
	cli, closeFn, err := openScheddClientFromEnv()
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, err)
		return 1
	}
	defer closeFn()
	snapIDs, err := cli.ForceRestartInstance(context.Background(), instanceID, reason, traceID)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl instances force-restart:", err)
		if len(snapIDs) > 0 {
			_, _ = fmt.Fprintf(osStderr, "gregalectl instances force-restart: snap_ids_marked_stale=%v — next wake will be a cold boot despite the destroy error\n", snapIDs)
		}
		return 1
	}
	_, _ = fmt.Fprintf(osStdout, "force-restart %s reason=%s trace_id=%s snap_ids=%v\n", instanceID, reason, traceID, snapIDs)
	return 0
}
