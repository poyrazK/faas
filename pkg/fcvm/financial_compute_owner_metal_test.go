//go:build linux && metal

package fcvm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

const financialNativePrefix = "financial-native-event:"

type financialNativeConfig struct {
	Traffic                        *FinancialPairTrafficOptions
	JobID, JobRunID, JobLeaseToken string
	// Configuration travels over stdin, never process arguments or test logs.
	DBURL, Database                          string
	RuntimeParams                            map[string]string
	ProxyHost                                string
	ProxyPort                                uint16
	PeerTarget                               string `json:"-"`
	NodeID, AccountID, AppID                 string
	Instance, Attempt, Root, JailRoot, Phase string
}

type financialNativeCommand struct{ Operation string }

type financialNativeEvent struct {
	RPCTarget, Veth           string
	InterfaceBytes            uint64
	InterfaceIndex            int
	CounterAvailable          bool
	Operation                 string
	Error, Detail             string
	Identity                  runtimeadmission.Identity
	Live, Leased, Physical    int
	Journal, ResourcesRemoved bool
	Host                      financialNativeHost
}

type financialNativeOwner struct {
	config   financialNativeConfig
	identity runtimeadmission.Identity
	ready    financialNativeEvent
	cmd      *exec.Cmd
	input    io.WriteCloser
	events   chan financialNativeEvent
	done     chan struct{}
	err      error // read only after done
	stderr   bytes.Buffer
	killed   bool
}

func startFinancialNativeOwner(t *testing.T, ctx context.Context, cfg financialNativeConfig) *financialNativeOwner {
	t.Helper()
	owner, err := launchFinancialNativeOwner(ctx, cfg)
	if err != nil {
		t.Fatalf("start native financial owner: %v", err)
	}
	return owner
}

func launchFinancialNativeOwner(ctx context.Context, cfg financialNativeConfig) (*financialNativeOwner, error) {
	// TestMain and crash checkpoints cannot run Go cleanup after SIGKILL.
	// Keep their disposable files inside the explicitly owned fixture tree.
	tmpDir := filepath.Join(cfg.Root, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return nil, err
	}
	owner := &financialNativeOwner{config: cfg, events: make(chan financialNativeEvent, 16), done: make(chan struct{})}
	owner.cmd = exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFinancialComputeNativeOwnerProcess$", "-test.timeout=9m")
	if cfg.PeerTarget != "" {
		var err error
		owner.cmd, err = financialPeerCommand(ctx, cfg.PeerTarget, "start", cfg.Instance)
		if err != nil {
			return nil, err
		}
	}
	owner.cmd.Env = append(os.Environ(), "FAAS_FINANCIAL_NATIVE_OWNER_CHILD=1", "TMPDIR="+tmpDir)
	owner.cmd.Stderr = &owner.stderr
	var err error
	owner.input, err = owner.cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := owner.cmd.StdoutPipe()
	if err != nil {
		_ = owner.input.Close()
		return nil, err
	}
	if err := owner.cmd.Start(); err != nil {
		_ = owner.input.Close()
		return nil, err
	}
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if payload, ok := strings.CutPrefix(scanner.Text(), financialNativePrefix); ok {
				var event financialNativeEvent
				if err := json.Unmarshal([]byte(payload), &event); err == nil {
					owner.events <- event
				}
			}
		}
		close(owner.events)
	}()
	go func() {
		owner.err = owner.cmd.Wait()
		close(owner.done)
	}()
	if err := json.NewEncoder(owner.input).Encode(cfg); err != nil {
		owner.terminate()
		return nil, err
	}
	readyCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	event, err := owner.next(readyCtx)
	if err != nil || event.Operation != "ready" || event.Error != "" {
		owner.terminate()
		if err == nil {
			err = errors.New("unexpected native startup response")
		}
		return nil, fmt.Errorf("owner not ready: operation=%s error=%s detail=%s transport=%w; %s", event.Operation, event.Error, event.Detail, err, owner.stderr.String())
	}
	owner.ready, owner.identity = event, event.Identity
	return owner, nil
}

func (o *financialNativeOwner) send(t *testing.T, command financialNativeCommand) {
	t.Helper()
	if err := json.NewEncoder(o.input).Encode(command); err != nil {
		t.Fatal("native owner command:", err)
	}
}

func (o *financialNativeOwner) next(ctx context.Context) (financialNativeEvent, error) {
	select {
	case event, ok := <-o.events:
		if !ok {
			return event, errors.New("native owner exited before responding")
		}
		return event, nil
	case <-ctx.Done():
		return financialNativeEvent{}, ctx.Err()
	}
}

func (o *financialNativeOwner) receive(t *testing.T) financialNativeEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	e, err := o.next(ctx)
	if err != nil {
		t.Fatal("native owner response:", err)
	}
	return e
}

func (o *financialNativeOwner) exchange(ctx context.Context, command financialNativeCommand) (financialNativeEvent, error) {
	if err := json.NewEncoder(o.input).Encode(command); err != nil {
		return financialNativeEvent{}, err
	}
	return o.next(ctx)
}

func (o *financialNativeOwner) call(t *testing.T, command financialNativeCommand) financialNativeEvent {
	t.Helper()
	o.send(t, command)
	e := o.receive(t)
	if e.Operation != command.Operation {
		t.Fatalf("unexpected native event for %s: %+v", command.Operation, e)
	}
	return e
}

func (o *financialNativeOwner) must(t *testing.T, operation string) financialNativeEvent {
	t.Helper()
	e := o.call(t, financialNativeCommand{Operation: operation})
	if e.Error != "" {
		t.Fatalf("native owner %s: %+v", operation, e)
	}
	return e
}

func (o *financialNativeOwner) wait(t *testing.T, deadline time.Time, condition func(financialNativeEvent) bool) {
	t.Helper()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	var last financialNativeEvent
	for time.Now().Before(deadline) {
		last = o.must(t, "status")
		if condition(last) {
			return
		}
		select {
		case <-t.Context().Done():
			t.Fatal("native owner observation cancelled")
		case <-tick.C:
		}
	}
	t.Fatalf("native owner missed physical deadline: %+v", last)
}

func (o *financialNativeOwner) terminate() {
	_ = o.input.Close()
	if o.config.PeerTarget != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if cmd, err := financialPeerCommand(ctx, o.config.PeerTarget, "kill", o.config.Instance); err == nil {
			_ = cmd.Run()
		}
	}
	_ = o.cmd.Process.Kill()
	<-o.done
	o.killed = true
}

func (o *financialNativeOwner) kill(t *testing.T) {
	t.Helper()
	if o.killed {
		return
	}
	if o.config.PeerTarget != "" {
		o.killPeer(t)
		return
	}
	// SIGKILL is essential: running Go cleanup would not exercise orphaned
	// native resources or release of process-lifetime journal locks.
	if err := o.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("kill native financial owner: %v", err)
	}
	_ = o.input.Close()
	select {
	case <-o.done:
		var exit *exec.ExitError
		if o.err == nil {
			t.Error("native owner exited cleanly before the requested crash")
		} else if !errors.As(o.err, &exit) || exit.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
			t.Errorf("native owner exited without the requested signal: %v; %s", o.err, o.stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Error("native owner did not exit after SIGKILL")
	}
	o.killed = true
}

// The helper is selected explicitly by the parent. Ordinary metal runs may
// skip this entry point; it is not itself an acceptance verdict.
func TestFinancialComputeNativeOwnerProcess(t *testing.T) {
	if os.Getenv("FAAS_FINANCIAL_NATIVE_OWNER_CHILD") != "1" {
		t.Skip("subprocess helper for PostgreSQL/native financial acceptance")
	}
	decoder := json.NewDecoder(os.Stdin)
	var cfg financialNativeConfig
	if err := decoder.Decode(&cfg); err != nil {
		t.Fatal("read native financial fixture configuration")
	}
	if err := financialPairOwnedPaths(cfg); err != nil {
		t.Fatal(err)
	}
	// adr: 1750 — production pre-creates vmmd's private native staging roots
	// (cmd/vmmd/config.go); recovery refuses a missing root, so the fixture
	// creates its own.
	for _, dir := range []string{"images", "publications"} {
		if err := os.MkdirAll(filepath.Join(cfg.Root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	poolCfg, err := financialNativePoolConfig(cfg)
	if err != nil {
		t.Fatal("invalid private PostgreSQL fixture configuration")
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatal("open private PostgreSQL fixture")
	}
	defer pool.Close()
	store := state.NewPgStore(pool)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	v := newMetalVMM(t, 30*time.Second)
	v.chrootBase = cfg.JailRoot
	v.WithNativeImageStagingRoot(filepath.Join(cfg.Root, "images")).
		WithNativeSnapshotPublicationRoot(filepath.Join(cfg.Root, "publications")).WithNativeProcessRecovery().
		// A replacement owner retires a killed one's VMs only through its
		// owner record (ADR-2026101710 Amendment 1).
		WithNativeLaunchOwners()
	var failKill atomic.Bool
	v.nativeRecovery.retirer.open = func(pid int) (nativeProcessHandle, error) {
		if failKill.Load() {
			return nil, errors.New("acceptance: native pidfd unavailable")
		}
		return openNativeProcess(pid)
	}
	m := NewManager(wire.ExecRunner{}, v, Paths{Kernel: os.Getenv("FAAS_TEST_KERNEL")}, os.Getenv("FAAS_TEST_FC_VERSION"), nil, nil).
		WithRuntimeAdmissionNodeID(cfg.NodeID).WithLifecycleContext(ctx)
	var outputMu sync.Mutex
	emit := func(event financialNativeEvent) {
		outputMu.Lock()
		defer outputMu.Unlock()
		payload, marshalErr := json.Marshal(event)
		if marshalErr != nil {
			t.Error(marshalErr)
			return
		}
		fmt.Printf("%s%s\n", financialNativePrefix, payload)
	}
	status := func(operation string, operationErr error) financialNativeEvent {
		e := financialNativeEvent{Operation: operation, Error: financialNativeError(operationErr), Live: m.LiveCount(), Leased: m.LeasedCount()}
		if cfg.Traffic != nil {
			if target, targetErr := m.PublicForwardTarget(cfg.Instance); targetErr == nil && target != nil {
				e.Veth = target.Veth
				e.InterfaceIndex = target.IfIndex
				if value, readErr := m.readFinancialEgress(target.Veth); readErr == nil {
					e.InterfaceBytes, e.CounterAvailable = value, true
				}
			}
		}
		if operationErr != nil {
			e.Detail = operationErr.Error()
		}
		record, readErr := v.nativeRecovery.journal.read(cfg.Instance)
		if errors.Is(readErr, os.ErrNotExist) {
			e.ResourcesRemoved = true
		} else if readErr != nil {
			e.Error, e.Detail = "inventory", readErr.Error()
		} else {
			e.Journal = true
			e.Physical = 1 // prepared resources are physical ownership too
			if record.ResourcesRemoved && record.ExitConfirmed {
				if err := v.nativeResourcesRemoved(record.Lease, nativeLeaseNetwork(record.Lease)); err == nil { //nolint:contextcheck // This native proof API owns its bounded drive-staging context and accepts no context parameter.
					processes, probeErr := v.nativeRecovery.retirer.probe.processes(ctx)
					if probeErr == nil {
						e.ResourcesRemoved, e.Physical = true, 0
						for _, process := range processes {
							if process.Instance == cfg.Instance {
								e.ResourcesRemoved, e.Physical = false, 1
							}
						}
					}
				}
			}
		}
		return e
	}
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		emit(financialNativeEvent{Operation: "ready", Error: "recovery", Detail: err.Error()})
		return
	}
	identity, err := m.RuntimeAdmissionIdentity()
	if err == nil {
		err = store.RegisterComputeNodeRuntimeIdentity(ctx, identity)
	}
	if err == nil {
		err = store.RegisterFinancialComputeOwner(ctx, identity.NodeID, identity.Incarnation)
	}
	if err != nil {
		emit(financialNativeEvent{Operation: "ready", Error: "identity", Detail: err.Error()})
		return
	}
	authority := &financialNativeAuthority{store: store}
	m.WithFinancialComputeAuthority(authority, identity.NodeID, identity.Incarnation)
	// Test-only checkpoints persist the actual production journal before
	// pausing. Killing the owner drops its gate pipe and OS locks naturally.
	var checkpoint sync.Once
	block := func() {
		checkpoint.Do(func() {
			emit(status("checkpoint", nil))
			<-ctx.Done()
		})
	}
	if cfg.Phase == "boot" {
		v.nativeRecovery.journal.writeRecord = func(path string, record nativeLaunchRecord) error {
			if err := writeNativeLaunchRecord(path, record); err != nil {
				return err
			}
			if record.Authorized && !record.Revoked {
				block()
			}
			return ctx.Err()
		}
	}
	if cfg.Phase == "teardown" {
		authority.beforeSettle = block
	}
	go func() {
		tick := time.NewTicker(api.FinancialComputeOwnerHeartbeat)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				callCtx, done := context.WithTimeout(ctx, api.FinancialComputeAuthorityTimeout)
				_ = store.RegisterFinancialComputeOwner(callCtx, identity.NodeID, identity.Incarnation)
				done()
			}
		}
	}()
	ready := status("ready", nil)
	if cfg.Traffic != nil {
		if FinancialPairServeRPC == nil {
			t.Fatal("native traffic RPC wiring is unavailable")
		}
		target, stop, startErr := FinancialPairServeRPC(ctx, m, *cfg.Traffic)
		if startErr != nil {
			t.Fatal("native traffic RPC startup:", startErr)
		}
		defer stop()
		ready.RPCTarget = target
	}
	ready.Identity = identity
	ready.Host = financialNativeHostFacts()
	emit(ready)
	for {
		var command financialNativeCommand
		if err := decoder.Decode(&command); err != nil {
			return
		}
		var err error
		switch command.Operation {
		case "wake":
			req := admissionWake(cfg.Instance)
			if cfg.Traffic != nil {
				req.Port = 9000 // dedicated multi-protocol acceptance guest
			}
			req.AccountID, req.AppID = cfg.AccountID, cfg.AppID
			req.BaseKey, req.LayerKey = os.Getenv("FAAS_TEST_BASE_ROOTFS"), os.Getenv("FAAS_TEST_LAYER_ROOTFS")
			if cfg.Traffic != nil {
				req.LayerKey = filepath.Join(os.Getenv("FAAS_FINANCIAL_PAIR_BUNDLE"), "traffic.ext4")
			}
			// Cancel the RPC immediately after its result. The native deadline
			// must continue on the daemon lifetime, not this request context.
			wakeCtx, done := context.WithCancel(wire.WithContext(ctx, wire.CorrelationFields{AppID: cfg.AppID, InstanceID: cfg.Instance, WakeID: cfg.Attempt}))
			if cfg.JobRunID != "" {
				_, err = m.BootJob(wakeCtx, JobBootRequest{Instance: cfg.Instance, AccountID: cfg.AccountID, NodeID: cfg.NodeID,
					RunID: cfg.JobRunID, TaskIndex: 0, LeaseToken: cfg.JobLeaseToken, Plan: api.PlanHobby,
					KernelKey: os.Getenv("FAAS_TEST_KERNEL"), BaseKey: os.Getenv("FAAS_TEST_BASE_ROOTFS"), ImageRef: os.Getenv("FAAS_TEST_LAYER_ROOTFS"),
					Command: []string{"/bin/sh", "-c", "sleep 3600"}, VcpuCount: 1, MemSizeMiB: 128, TaskTimeoutSec: 300})
			} else {
				_, err = m.Wake(wakeCtx, req)
			}
			done()
		case "destroy":
			err = m.Destroy(ctx, cfg.Instance)
		case "offline":
			pool.Close()
		case "settlement-off":
			authority.failSettlement.Store(true)
		case "settlement-on":
			authority.failSettlement.Store(false)
		case "kill-off":
			failKill.Store(true)
		case "recover":
			var leases []financial.ComputeLease
			leases, err = store.FinancialComputeNodeLeases(ctx, cfg.NodeID)
			if err == nil {
				m.ForgetUnfundedFinancialComputeAttempts(leases)
				for _, lease := range leases {
					err = errors.Join(err, m.RecoverFinancialComputeLease(ctx, lease))
				}
			}
		case "status":
		default:
			err = errors.New("unknown native financial command")
		}
		emit(status(command.Operation, err))
	}
}

func financialNativePoolConfig(cfg financialNativeConfig) (*pgxpool.Config, error) {
	pool, err := pgxpool.ParseConfig(cfg.DBURL)
	if err != nil {
		return nil, errors.New("invalid private PostgreSQL fixture configuration")
	}
	// ConnString retains the original DSN. Copy actual clone/schema identity,
	// and redirect every TLS/address fallback through the fault proxy too.
	pool.ConnConfig.Database = cfg.Database
	pool.ConnConfig.RuntimeParams = cfg.RuntimeParams
	if cfg.ProxyHost != "" {
		pool.ConnConfig.Host, pool.ConnConfig.Port = cfg.ProxyHost, cfg.ProxyPort
		for _, fallback := range pool.ConnConfig.Fallbacks {
			fallback.Host, fallback.Port = cfg.ProxyHost, cfg.ProxyPort
		}
	}
	return pool, nil
}

// A narrow fault seam around a real PgStore; it neither embeds a store nor
// invents grants, timestamps, rates, reservations or retirement receipts.
type financialNativeAuthority struct {
	store          *state.PgStore
	failSettlement atomic.Bool
	beforeSettle   func()
}

func (a *financialNativeAuthority) CancelFinancialCompute(ctx context.Context, r financial.ComputeRequest) (financial.ComputeLease, error) {
	return a.store.CancelFinancialCompute(ctx, r)
}
func (a *financialNativeAuthority) FinancialComputeRetired(ctx context.Context, node, instance string) (bool, error) {
	return a.store.FinancialComputeRetired(ctx, node, instance)
}
func (a *financialNativeAuthority) FinancialComputeLease(ctx context.Context, account, id string) (financial.ComputeLease, bool, error) {
	return a.store.FinancialComputeLease(ctx, account, id)
}
func (a *financialNativeAuthority) AcquireFinancialCompute(ctx context.Context, r financial.ComputeRequest) (financial.ComputeLease, error) {
	return a.store.AcquireFinancialCompute(ctx, r)
}
func (a *financialNativeAuthority) RenewFinancialCompute(ctx context.Context, r financial.ComputeRequest) (financial.ComputeLease, error) {
	return a.store.RenewFinancialCompute(ctx, r)
}
func (a *financialNativeAuthority) SettleFinancialCompute(ctx context.Context, r financial.ComputeRequest) (financial.ComputeLease, error) {
	if a.beforeSettle != nil {
		a.beforeSettle()
	}
	if a.failSettlement.Load() {
		return financial.ComputeLease{}, errors.New("acceptance: settlement unavailable")
	}
	return a.store.SettleFinancialCompute(ctx, r)
}
