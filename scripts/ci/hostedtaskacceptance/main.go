//go:build hosted_task_acceptance && linux && amd64

// Command hostedtaskacceptance runs one owner-approved rehearsal from the
// stamped native acceptance host without starting API listeners.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"os/user"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/taskrehearsal"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal/native"
	"github.com/onebox-faas/faas/pkg/wire"
)

const (
	scheddSocket = "unix:///run/faas/schedd.sock"
	vmmdSocket   = "unix:///run/faas/vmmd.sock"
)

type enrollment struct {
	AccountID string `json:"account_id"`
	NodeID    string `json:"node_id"`
	APIOrigin string `json:"api_origin"`
	Dedicated bool   `json:"dedicated_native_host"`
}

func main() {
	var fixture taskrehearsal.StartupFixture
	var archives tasktoolrehearsal.FixtureArchives
	intent := flag.String("intent", "", "approved hosted source intent ID")
	plan := flag.String("plan", "", "approved rehearsal plan SHA-256")
	flag.StringVar(&fixture.WebSHA256, "web-source", "", "prepared web source SHA-256")
	flag.StringVar(&fixture.WorkerSHA256, "worker-source", "", "prepared worker source SHA-256")
	flag.StringVar(&archives.Web, "web-archive", "", "prepared web archive SHA-256")
	flag.StringVar(&archives.Worker, "worker-archive", "", "prepared worker archive SHA-256")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, tasktoolrehearsal.WorkloadLease+time.Minute)
	defer cancel()

	if err := execute(ctx, *intent, *plan, fixture, archives, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func execute(ctx context.Context, intent, plan string, fixture taskrehearsal.StartupFixture, archives tasktoolrehearsal.FixtureArchives, out io.Writer) error {
	identity, err := enrolledHost()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	receipt, err := apidserver.RunHostedTaskOperatorRecovery(ctx, identity.AccountID, os.Getenv("FAAS_TOKEN"), intent, plan, archives,
		func(ctx context.Context, store *state.PgStore) (*taskrehearsal.NativeRecovery, func(), error) {
			return nativeDriver(ctx, identity, fixture, store, log)
		}, log)
	if err != nil {
		return err
	}
	if err := checkEnrollment(identity); err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(receipt)
}

func enrolledHost() (enrollment, error) {
	var identity enrollment
	if os.Getenv("GREGALE_REHEARSAL_NATIVE_ACCEPTANCE") != "1" || os.Getenv("FAAS_TEST_SKIP_POSTGRES") == "1" ||
		os.Getenv("FAAS_TEST_FAKE_STORAGE") == "1" {
		return identity, errors.New("native acceptance opt-in and real backends are required")
	}
	uid, err := user.Current()
	if err != nil || uid.Username != "faas-apid" || os.Geteuid() == 0 {
		return identity, errors.New("run the acceptance child as the faas-apid service identity")
	}
	kvm, err := os.Stat("/dev/kvm")
	if err != nil || kvm.Mode()&os.ModeCharDevice == 0 {
		return identity, errors.New("native KVM device is unavailable")
	}
	body, err := enrolledFile("/etc/faas/rehearsal-acceptance-host.json")
	if err != nil || json.Unmarshal(body, &identity) != nil || !identity.Dedicated ||
		!canonicalID(identity.AccountID) || !canonicalID(identity.NodeID) || identity.APIOrigin == "" ||
		identity.APIOrigin != strings.TrimSuffix(os.Getenv("FAAS_API"), "/") {
		return enrollment{}, errors.New("invalid dedicated hosted Task host enrollment")
	}
	if err := checkBuildStamp(); err != nil {
		return enrollment{}, err
	}
	return identity, nil
}

func enrolledFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || info.Size() > 16<<10 {
		return nil, errors.New("invalid native enrollment file")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != 0 {
		return nil, errors.New("native enrollment file is not root-owned")
	}
	//nolint:gosec // only the two fixed root-owned enrollment paths reach this reader.
	return os.ReadFile(path)
}

func checkBuildStamp() error {
	stamp, err := enrolledFile("/etc/faas/rehearsal-acceptance-platform-sha")
	build, ok := debug.ReadBuildInfo()
	if err != nil || !ok {
		return errors.New("read native platform and acceptance build stamps")
	}
	var revision, modified string
	for _, setting := range build.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		}
	}
	if len(revision) != 40 || modified != "false" || revision != strings.TrimSpace(string(stamp)) {
		return errors.New("acceptance build does not match the native platform release")
	}
	return nil
}

func checkEnrollment(expected enrollment) error {
	current, err := enrolledHost()
	if err != nil {
		return err
	}
	if current != expected {
		return errors.New("native host enrollment changed during the journey")
	}
	return nil
}

func nativeDriver(ctx context.Context, identity enrollment, fixture taskrehearsal.StartupFixture, store *state.PgStore,
	log *slog.Logger) (*taskrehearsal.NativeRecovery, func(), error) {
	checkHost := func(ctx context.Context, account, nodeID string) error {
		if account != identity.AccountID || nodeID != identity.NodeID {
			return state.ErrConflict
		}
		if err := checkEnrollment(identity); err != nil {
			return err
		}
		node, err := store.ComputeNodeByID(ctx, nodeID)
		if err != nil || !node.Active || node.ScheddTargetURL == nil ||
			*node.ScheddTargetURL != scheddSocket || node.TargetURL != vmmdSocket {
			return errors.New("enrolled node must own the local schedd and vmmd sockets")
		}
		return nil
	}
	if err := checkHost(ctx, identity.AccountID, identity.NodeID); err != nil {
		return nil, nil, err
	}
	for _, target := range []string{scheddSocket, vmmdSocket} {
		info, err := os.Stat(strings.TrimPrefix(target, "unix://"))
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			return nil, nil, errors.New("enrolled local scheduler or VM socket is unavailable")
		}
	}
	sched, err := scheddgrpc.DialContext(ctx, scheddSocket, nil)
	if err != nil {
		return nil, nil, err
	}
	vm, err := wire.DialContext(ctx, vmmdSocket, nil)
	if err != nil {
		_ = sched.Close()
		return nil, nil, err
	}
	closeClients := func() { _ = sched.Close(); _ = vm.Close() }
	client := vmmdpb.NewVmmdClient(vm)
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	_, err = client.Ping(probe, &vmmdpb.PingRequest{})
	cancel()
	if err != nil {
		closeClients()
		return nil, nil, errors.New("verify the enrolled local vmmd is reachable")
	}
	driver, err := native.Recovery(store, sched, nodeClient{identity.NodeID, client}, identity.NodeID, fixture, checkHost, log)
	if err != nil {
		closeClients()
		return nil, nil, err
	}
	return driver, closeClients, nil
}

type nodeClient struct {
	node string
	vm   vmmdpb.VmmdClient
}

func (n nodeClient) ClientFor(_ context.Context, node string) (vmmdpb.VmmdClient, io.Closer, bool) {
	if node != n.node {
		return nil, nil, false
	}
	// The runner owns the connection lifetime; each forwarded request
	// holds a non-owning lease.
	return n.vm, io.NopCloser(strings.NewReader("")), true
}

func canonicalID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}
