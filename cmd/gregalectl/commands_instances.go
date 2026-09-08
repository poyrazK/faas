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
	action  string
	target  string
	reason  string
	traceID string
	timeout time.Duration
}

func cmdInstancesDispatch(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl instances: missing subcommand; want force-park|force-cold-boot|force-restart")
		return 2
	}
	switch args[0] {
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
		if !instanceBreakGlassAllowed("force-park", *reason) {
			return 2
		}
		return forceParkBreakGlass(*instanceID, strings.TrimSpace(*reason), traceID)
	}
	return mutateInstanceViaAPI(instanceMutationSpec{
		action: "force-park", target: *instanceID,
		reason:  instanceMutationReason(*reason, "operator_force_park"),
		traceID: traceID, timeout: *timeout,
	})
}

func cmdInstancesForceColdBoot(args []string) int {
	fs := flag.NewFlagSet("force-cold-boot", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	appSlug := fs.String("app-slug", "", "app slug whose latest deployment will be cold-booted on next wake")
	reason := fs.String("reason", "", "audit reason slug ([a-z0-9_]{1,64})")
	timeout := fs.Duration("timeout", instanceMutationDefaultTimeout, "maximum durable-intent wait")
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
		if !instanceBreakGlassAllowed("force-cold-boot", *reason) {
			return 2
		}
		return forceColdBootBreakGlass(*appSlug, strings.TrimSpace(*reason), traceID)
	}
	return mutateInstanceViaAPI(instanceMutationSpec{
		action: "force-cold-boot", target: *appSlug,
		reason:  instanceMutationReason(*reason, "operator_force_cold_boot"),
		traceID: traceID, timeout: *timeout,
	})
}

func cmdInstancesForceRestart(args []string) int {
	fs := flag.NewFlagSet("force-restart", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	instanceID := fs.String("instance-id", "", "instance id (uuid) to force-restart (kill + cold-boot on next wake)")
	reason := fs.String("reason", "", "audit reason slug ([a-z0-9_]{1,64})")
	timeout := fs.Duration("timeout", instanceMutationDefaultTimeout, "maximum durable-intent wait")
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
		if !instanceBreakGlassAllowed("force-restart", *reason) {
			return 2
		}
		return forceRestartBreakGlass(*instanceID, strings.TrimSpace(*reason), traceID)
	}
	return mutateInstanceViaAPI(instanceMutationSpec{
		action: "force-restart", target: *instanceID,
		reason:  instanceMutationReason(*reason, "operator_force_restart"),
		traceID: traceID, timeout: *timeout,
	})
}

func instanceTraceID(raw string) string {
	if strings.TrimSpace(raw) != "" {
		return strings.TrimSpace(raw)
	}
	return wire.NewTraceID()
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

	headers := make(http.Header)
	headers.Set(operatorTraceIDHeader, spec.traceID)
	var accepted api.OperatorIntentAcceptedResponse
	if err := client.doJSONWithHeaders(ctx, http.MethodPost, path, nil, &accepted, true, nil, headers); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl instances %s: %v\n", spec.action, err)
		return 1
	}
	if accepted.IntentID == "" || accepted.StatusURL == "" {
		_, _ = fmt.Fprintf(osStderr, "gregalectl instances %s: apid returned an incomplete intent receipt\n", spec.action)
		return 1
	}

	var outcome api.OperatorIntentResponse
	for {
		if err := client.doJSON(ctx, http.MethodGet, accepted.StatusURL, nil, &outcome, false, nil); err != nil {
			_, _ = fmt.Fprintf(osStderr, "gregalectl instances %s: poll intent %s: %v\n", spec.action, accepted.IntentID, err)
			return 1
		}
		switch state.OperatorIntentStatus(outcome.Status) {
		case state.OperatorIntentSucceeded:
			return renderInstanceMutationSuccess(spec, accepted, outcome)
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

func renderInstanceMutationSuccess(spec instanceMutationSpec, accepted api.OperatorIntentAcceptedResponse, outcome api.OperatorIntentResponse) int {
	if jsonEnabled() {
		return emitOperatorJSON(struct {
			Operation api.OperatorIntentAcceptedResponse `json:"operation"`
			Outcome   api.OperatorIntentResponse         `json:"outcome"`
		}{accepted, outcome})
	}
	traceID := outcome.TraceID
	if traceID == "" {
		traceID = spec.traceID
	}
	_, _ = fmt.Fprintf(osStdout, "%s succeeded target=%s intent=%s trace_id=%s", spec.action, spec.target, accepted.IntentID, traceID)
	if accepted.DeploymentID != "" {
		_, _ = fmt.Fprintf(osStdout, " deployment=%s", accepted.DeploymentID)
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
