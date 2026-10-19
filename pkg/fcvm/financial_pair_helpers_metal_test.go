//go:build linux && metal

package fcvm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresfixture"
)

// Host identities come from the executing owner, never from compute_nodes.
type financialNativeHost struct {
	MachineID string `json:"machine_id_sha256"`
	BootID    string `json:"boot_id_sha256"`
	Arch      string `json:"architecture"`
	Root      bool   `json:"root"`
	KVM       bool   `json:"kvm"`
}

func financialNativeHostFacts() financialNativeHost {
	hash := func(path string) string {
		data, err := os.ReadFile(path)
		if err != nil || strings.TrimSpace(string(data)) == "" {
			return ""
		}
		h := sha256.Sum256([]byte(strings.TrimSpace(string(data))))
		return hex.EncodeToString(h[:])
	}
	info, err := os.Stat("/dev/kvm")
	return financialNativeHost{MachineID: hash("/etc/machine-id"), BootID: hash("/proc/sys/kernel/random/boot_id"), Arch: runtime.GOARCH, Root: os.Geteuid() == 0, KVM: err == nil && info.Mode()&os.ModeCharDevice != 0}
}

func financialPairJailDirectory(parent string) (string, error) {
	// A versioned Firecracker basename, UUID, vsock port suffix and n0/n1
	// already use most of sockaddr_un. MkdirTemp's long decimal suffix would
	// exceed it beneath the run namespace. Creation remains exclusive.
	for range 16 {
		path := filepath.Join(parent, "c"+uuid.NewString()[:4])
		if err := os.Mkdir(path, 0o700); errors.Is(err, os.ErrExist) {
			continue
		} else if err != nil {
			return "", err
		}
		return path, nil
	}
	return "", errors.New("native pair short jail namespace is occupied")
}

func financialPeerCommand(ctx context.Context, target, operation, argument string) (*exec.Cmd, error) {
	// SSH joins its trailing arguments into a remote shell command. Accept
	// only this fixed command surface and shell-inert staging identifiers.
	bundle := os.Getenv("FAAS_FINANCIAL_PAIR_BUNDLE")
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]*$`).MatchString(target) ||
		!regexp.MustCompile(`^/var/tmp/faas-financial-pair-[a-f0-9]{40}-[A-Za-z0-9._-]+$`).MatchString(bundle) ||
		!regexp.MustCompile(`^[A-Za-z0-9/._-]+$`).MatchString(argument) {
		return nil, errors.New("invalid native pair SSH target, bundle or argument")
	}
	switch operation {
	case "start", "kill", "remove":
	default:
		return nil, errors.New("unsupported native pair operation")
	}
	return exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=10", "-o", "ServerAliveCountMax=3", "--", target, "sudo", "-n", "bash", bundle+"/financial-pair-host.sh", operation, bundle, argument), nil
}

func financialPairOwnedPaths(cfg financialNativeConfig) error {
	if os.Getenv("FAAS_FINANCIAL_PAIR") != "1" {
		return nil
	}
	for _, pair := range [][2]string{{cfg.Root, os.Getenv("FAAS_FINANCIAL_ACCEPTANCE_ROOT")}, {cfg.JailRoot, os.Getenv("FAAS_FINANCIAL_PAIR_JAIL_ROOT")}} {
		if pair[1] == "" || !strings.HasPrefix(filepath.Clean(pair[0]), filepath.Clean(pair[1])+string(os.PathSeparator)) {
			return errors.New("native pair owner path outside its private fixture")
		}
		if err := os.MkdirAll(pair[0], 0o700); err != nil {
			return err
		}
	}
	if !strings.HasPrefix(cfg.Database, "faas_test_clone_") || net.ParseIP(cfg.ProxyHost) == nil || cfg.ProxyPort == 0 {
		return errors.New("native pair requires a private pgtest database and an explicit SQL proxy")
	}
	return nil
}

func (f *financialNativeFixture) configurePair(pool *pgxpool.Pool) {
	if os.Getenv("FAAS_FINANCIAL_PAIR") != "1" {
		return
	}
	addr := os.Getenv("FAAS_FINANCIAL_PAIR_ADDR_A")
	if ip := net.ParseIP(addr); ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		f.t.Fatal("native pair requires node A's dedicated, reachable fixture IP")
	}
	for i := range f.configs {
		f.proxies[i] = postgresfixture.OpenProxy(f.t, pool, addr, 0)
		f.configs[i].ProxyHost, f.configs[i].ProxyPort = f.proxies[i].Host, f.proxies[i].Port
	}
	f.configs[1].PeerTarget = os.Getenv("FAAS_FINANCIAL_PAIR_SSH_B")
	if f.configs[1].PeerTarget == "" {
		f.t.Fatal("native pair SSH peer is missing")
	}
}

func (f *financialNativeFixture) recordPairOwner(node int, owner *financialNativeOwner) {
	if os.Getenv("FAAS_FINANCIAL_PAIR") != "1" {
		return
	}
	host := owner.ready.Host
	local := financialNativeHostFacts()
	expected := local.MachineID
	if node == 1 {
		expected = os.Getenv("FAAS_FINANCIAL_PAIR_MACHINE_B")
		if expected == local.MachineID || host.BootID == local.BootID {
			f.t.Fatal("native pair resolved to the same physical host/boot")
		}
	}
	if len(expected) != 64 || host.MachineID != expected || len(host.BootID) != 64 || !host.Root || !host.KVM || host.Arch != "amd64" {
		f.t.Fatalf("native pair owner has no matching physical-host evidence: %+v", host)
	}
	record, err := json.Marshal(struct {
		Node     int                 `json:"node"`
		Instance string              `json:"instance"`
		Host     financialNativeHost `json:"host"`
	}{node, owner.config.Instance, host})
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Logf("financial-pair-owner:%s", record)
}

func (o *financialNativeOwner) killPeer(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd, err := financialPeerCommand(ctx, o.config.PeerTarget, "kill", o.config.Instance)
	if err == nil {
		err = cmd.Run()
	}
	if err != nil {
		t.Errorf("SIGKILL native pair owner: %v", err)
	}
	_ = o.input.Close()
	select {
	case <-o.done:
		if o.err == nil {
			t.Error("remote native owner exited cleanly before the requested crash")
		}
	case <-ctx.Done():
		_ = o.cmd.Process.Kill()
		<-o.done
		t.Error("remote native owner did not join after SIGKILL")
	}
	o.killed = true
}

func requireFinancialPair(t *testing.T) {
	t.Helper()
	if os.Getenv("FAAS_FINANCIAL_PAIR") != "1" {
		t.Skip("requires the guarded physical two-host financial runner")
	}
	if os.Getenv("FAAS_FINANCIAL_COMPUTE_ACCEPTANCE") != "1" {
		t.Fatal(fmt.Errorf("native financial component qualification must be enabled"))
	}
}
