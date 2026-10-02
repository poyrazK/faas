// adr: 398
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrRestartQuarantine means resources survived a previous vmmd, but this
// Manager cannot yet prove their lifecycle ownership. It must neither reuse
// their identities nor acknowledge their teardown.
var ErrRestartQuarantine = errors.New("vmmd restart ownership reconciliation required")

type restartInventoryOptions struct {
	procRoot, netRoot, netnsRoot, jailRoot string
}

type restartInventory struct {
	slots     map[int]struct{}
	instances map[string]struct{}
	processes int
}

// RestartQuarantineReport describes resources excluded from new allocation.
// These are observations, not recovered live Instances or teardown receipts.
type RestartQuarantineReport struct {
	Slots, Instances, Processes int
}

// RecoverRestartQuarantine inventories surviving Firecracker/jailer processes,
// slot-addressed links, namespaces and jails before any new leases or prepared
// networks are allocated. No resources are removed. Quarantine lasts for this
// Manager's lifetime, including when the existing durable-state sweep later
// removes an orphan. Reattachment and confirmed reclamation are separate work.
func (m *Manager) RecoverRestartQuarantine(ctx context.Context, jailRoot string) (RestartQuarantineReport, error) {
	return m.recoverRestartQuarantine(ctx, restartInventoryOptions{
		procRoot: "/proc", netRoot: "/sys/class/net", netnsRoot: "/run/netns", jailRoot: jailRoot,
	})
}

func (m *Manager) recoverRestartQuarantine(ctx context.Context, opts restartInventoryOptions) (RestartQuarantineReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.restartInventoryDone || len(m.instanceFlights)+len(m.instanceStops)+len(m.live)+len(m.pendingCleanup) != 0 || m.preparedNetworks != nil || !m.alloc.pristine() {
		return RestartQuarantineReport{}, errors.New("vmmd: restart inventory must precede instance and prepared-network operations")
	}
	inv, err := scanRestartInventory(ctx, opts)
	if err != nil {
		return RestartQuarantineReport{}, fmt.Errorf("vmmd: restart inventory: %w", err)
	}
	m.alloc.quarantine(inv.slots)
	m.restartQuarantine = inv.instances
	m.restartInventoryDone = true
	return RestartQuarantineReport{Slots: len(inv.slots), Instances: len(inv.instances), Processes: inv.processes}, nil
}

func scanRestartInventory(ctx context.Context, opts restartInventoryOptions) (restartInventory, error) {
	inv := restartInventory{slots: make(map[int]struct{}), instances: make(map[string]struct{})}
	if opts.procRoot == "" || opts.netRoot == "" || opts.netnsRoot == "" || opts.jailRoot == "" {
		return inv, errors.New("incomplete resource roots")
	}
	if err := inv.scanProcesses(ctx, opts.procRoot); err != nil {
		return inv, err
	}
	for _, root := range []string{opts.netRoot, opts.netnsRoot, opts.jailRoot} {
		entries, err := os.ReadDir(root)
		// /proc and sysfs are required; a node that has never created a VM
		// need not have a named namespace directory or versioned jail tree.
		if errors.Is(err, os.ErrNotExist) && root != opts.netRoot {
			continue
		}
		if err != nil {
			return inv, fmt.Errorf("read resources %s: %w", root, err)
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return inv, err
			}
			name := entry.Name()
			if root == opts.netRoot {
				if slot, ok := restartNetworkSlot(name); ok {
					inv.slots[slot] = struct{}{}
				}
				continue
			}
			if root == opts.netnsRoot {
				if !strings.HasPrefix(name, "fc-") || strings.HasPrefix(name, "fc-prepared-") {
					continue
				}
				name = strings.TrimPrefix(name, "fc-")
			}
			if restartResourceID(name) {
				inv.instances[name] = struct{}{}
			}
		}
	}
	return inv, ctx.Err()
}

func (inv *restartInventory) scanProcesses(ctx context.Context, root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("read processes: %w", err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil || !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name())
		raw, err := os.ReadFile(filepath.Join(path, "cmdline"))
		if errors.Is(err, os.ErrNotExist) {
			continue // Exited during enumeration.
		}
		if err != nil {
			return fmt.Errorf("read process %s argv: %w", entry.Name(), err)
		}
		args := strings.Split(string(raw), "\x00")
		id, jailer := restartProcessID(args)
		if id == "" {
			if jailer || strings.HasPrefix(filepath.Base(args[0]), "firecracker") {
				return fmt.Errorf("cannot identify surviving guest instance (pid %s)", entry.Name())
			}
			continue
		}
		// Firecracker no longer carries jailer's --uid after exec. Its
		// actual kernel UID is the surviving slot identity. For a jailer
		// still running as root, reserve the destination UID before exec.
		var slot int
		if jailer {
			slot = restartJailerSlot(args)
		} else {
			status, err := os.ReadFile(filepath.Join(path, "status"))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return fmt.Errorf("read process %s UID: %w", entry.Name(), err)
			}
			slot = restartProcessSlot(string(status))
		}
		if slot < 0 {
			return fmt.Errorf("cannot identify slot for surviving guest %s (pid %s)", id, entry.Name())
		}
		inv.slots[slot] = struct{}{}
		inv.instances[id] = struct{}{}
		inv.processes++
	}
	return nil
}

func restartJailerSlot(args []string) int {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--uid" {
			uid, err := strconv.Atoi(args[i+1])
			if err != nil || uid < JailUIDBase || uid > JailUIDMax {
				return -1
			}
			return uid - JailUIDBase
		}
	}
	return -1
}

func restartProcessID(args []string) (id string, jailer bool) {
	if len(args) == 0 {
		return "", false
	}
	name := filepath.Base(args[0])
	jailer = strings.HasPrefix(name, "jailer")
	if !jailer && !strings.HasPrefix(name, "firecracker") {
		return "", false
	}
	var executable string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--id":
			id = args[i+1]
		case "--exec-file":
			executable = filepath.Base(args[i+1])
		}
	}
	if jailer && !strings.HasPrefix(executable, "firecracker") {
		return "", false
	}
	if !restartResourceID(id) {
		return "", jailer
	}
	return id, jailer
}

// Builders use build-<id>; in-memory/internal callers also use compact IDs.
// This is observational quarantine, so do not apply the UUID-only kill gate
// from reap.go. Accept Firecracker's path-safe alphabet, without authorizing
// filesystem mutation or treating a name as reconstructed ownership.
func restartResourceID(id string) bool {
	if id == "" {
		return false
	}
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

func restartProcessSlot(status string) int {
	for _, line := range strings.Split(status, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 5 || fields[0] != "Uid:" {
			continue
		}
		uid, err := strconv.Atoi(fields[1])
		if err != nil || uid < JailUIDBase || uid > JailUIDMax {
			return -1
		}
		for _, field := range fields[2:] {
			if field != fields[1] {
				return -1
			}
		}
		return uid - JailUIDBase
	}
	return -1
}

func restartNetworkSlot(name string) (int, bool) {
	for _, prefix := range []string{"vh", "vp", "gpn-h", "gpn-p"} {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		slot, err := strconv.Atoi(strings.TrimPrefix(name, prefix))
		if err != nil || slot < 0 || slot >= MaxSlots {
			return 0, false
		}
		want := fmt.Sprintf("%s%d", prefix, slot)
		if strings.HasPrefix(prefix, "gpn-") {
			want = fmt.Sprintf("%s%05d", prefix, slot)
		}
		return slot, name == want
	}
	return 0, false
}
