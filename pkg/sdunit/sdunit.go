// Package sdunit starts, inspects and stops transient systemd services
// through PID 1's D-Bus API (org.freedesktop.systemd1). vmmd uses it to run
// each instance's bridge as a unit that systemd owns, so the bridge is never
// in vmmd's cgroup and outlives a vmmd restart (connection-survival ADR §2,
// slice 1c).
//
// Operations poll unit state instead of following job signals: a bridge
// starts or stops once per wake or park, and polling needs no Subscribe, no
// signal routing and no ordering between a job's reply and its JobRemoved.
package sdunit

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

// Service is a transient service's definition. Zero fields are left to
// systemd's defaults, except the sandbox, which every Service gets.
type Service struct {
	Description string
	// ExecStart is the argv; ExecStart[0] is an absolute path.
	ExecStart   []string
	Environment []string
	// UID and GID set User= and Group=. systemd resolves them through the
	// user database and refuses a uid with no account, so a service whose
	// uid has none leaves both 0, starts as root and drops to it itself;
	// Capabilities is then all it holds until it does.
	UID, GID int
	// Capabilities is the capability bounding set (a CAP_* bit mask).
	Capabilities uint64
	// NetworkNamespacePath joins the service to a bound network namespace.
	NetworkNamespacePath string
	Slice                string
	MemoryMaxBytes       uint64
	TasksMax             uint64
	LimitNOFILE          uint64
	// ReadWritePaths are the only writable paths under ProtectSystem=strict.
	ReadWritePaths []string
	// IPAddressAllow is the only IP traffic the service may send or
	// receive; everything else is denied (IPAddressDeny=any).
	IPAddressAllow []netip.Prefix
	// RestrictAddressFamilies lists the socket families the service may
	// create, such as AF_UNIX and AF_INET.
	RestrictAddressFamilies []string
	// TimeoutStop is how long a stop waits after SIGTERM before SIGKILL.
	TimeoutStop time.Duration
}

// Unit is a loaded unit's state.
type Unit struct {
	Name string
	// LoadState is "not-found" for a unit systemd does not know.
	LoadState   string
	ActiveState string
	// InvocationID identifies one run of the unit (hex, 32 characters).
	InvocationID string
	MainPID      int
	ControlGroup string
}

// Exists reports whether systemd has the unit loaded.
func (u Unit) Exists() bool { return u.LoadState != "" && u.LoadState != "not-found" }

// Active reports whether the unit runs.
func (u Unit) Active() bool { return u.ActiveState == "active" || u.ActiveState == "reloading" }

// Manager is the systemd API vmmd uses. *Systemd implements it.
type Manager interface {
	// StartService creates and starts the transient service name and waits
	// until it runs (Type=exec: its main process has exec'd) or failed.
	StartService(ctx context.Context, name string, svc Service) (Unit, error)
	// Stop stops the unit and waits until systemd has unloaded it, so its
	// cgroup is gone. A unit that does not exist is already stopped.
	Stop(ctx context.Context, name string) error
	// Unit returns the unit's state; LoadState is "not-found" when absent.
	Unit(ctx context.Context, name string) (Unit, error)
	// List returns the names of loaded units matching the glob pattern.
	List(ctx context.Context, pattern string) ([]string, error)
	Close() error
}

// Capability bits for Service.Capabilities.
const (
	CapSetGID uint64 = 1 << 6
	CapSetUID uint64 = 1 << 7
)

// ErrStartFailed is a service that did not reach the running state.
var ErrStartFailed = errors.New("transient service did not start")

var serviceNameRe = regexp.MustCompile(`^[A-Za-z0-9_.@:-]{1,240}\.service$`)

// ValidServiceName reports whether name is a service unit name this package
// creates: unit-name characters only, no escaping needed.
func ValidServiceName(name string) bool { return serviceNameRe.MatchString(name) }

func (s Service) validate() error {
	if len(s.ExecStart) == 0 || len(s.ExecStart[0]) == 0 || s.ExecStart[0][0] != '/' {
		return errors.New("sdunit: ExecStart needs an absolute path")
	}
	if s.UID < 0 || s.GID < 0 || (s.UID == 0) != (s.GID == 0) {
		return fmt.Errorf("sdunit: invalid credentials uid %d gid %d", s.UID, s.GID)
	}
	for _, p := range s.IPAddressAllow {
		if !p.IsValid() {
			return errors.New("sdunit: invalid IPAddressAllow prefix")
		}
	}
	for _, kv := range s.Environment {
		if name, _, ok := strings.Cut(kv, "="); !ok || !envNameRe.MatchString(name) {
			return fmt.Errorf("sdunit: invalid environment assignment %q", kv)
		}
	}
	return nil
}

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
