// Package dbguest is the host-guest contract for database microVMs
// (ADR-1027): the kernel command-line keys that select guest-init's database
// mode, the vsock ports it serves, and its control protocol. The package has
// no I/O so guest-init and the host side share one definition.
package dbguest

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	// ModeArg on the kernel command line selects guest-init's database mode.
	// The data volume is never used as an overlay upper in this mode.
	ModeArg = "gregale.mode=database"
	// MajorArgKey carries the PostgreSQL major version of the volume, so
	// guest-init starts the matching binaries and refuses a mismatched
	// cluster instead of upgrading it implicitly.
	MajorArgKey = "gregale.pg_major"

	// DataPort is the vsock port guest-init bridges to PostgreSQL's local
	// socket. Clients authenticate to PostgreSQL itself.
	DataPort uint32 = 5432
	// ControlPort serves the newline-delimited JSON control protocol.
	ControlPort uint32 = 5433
	// ArchivePort is the host vsock port guest-initiated archive streams
	// (WAL and base backups, pkg/dbarchive) reach; vmmd relays them to
	// dbbackupd.
	ArchivePort uint32 = 5434
	// ArchiveArg on the kernel command line turns WAL archiving on. vmmd
	// sets it only when it relays archive streams: archiving into nothing
	// would grow pg_wal without bound.
	ArchiveArg = "gregale.archive=1"

	// MinMajor and MaxMajor bound the supported PostgreSQL majors.
	MinMajor = 14
	MaxMajor = 18

	// MaxRequestBytes bounds one control request line.
	MaxRequestBytes = 64 << 10
	// MaxAdminSQLBytes bounds the SQL of one admin request.
	MaxAdminSQLBytes = 60 << 10
)

// Op is one control request.
type Op string

const (
	// OpStatus reports the database state without side effects.
	OpStatus Op = "status"
	// OpBootstrap initializes an empty volume. It is accepted once, while
	// the state is StateNeedsBootstrap, and carries no secret: the
	// superuser authenticates only by peer inside the guest.
	OpBootstrap Op = "bootstrap"
	// OpAdminSQL runs SQL as the superuser, stopping at the first error.
	// Batches that must be atomic wrap themselves in BEGIN/COMMIT. Only
	// the control plane reaches the control port; customers never do.
	OpAdminSQL Op = "admin_sql"
	// OpStop shuts PostgreSQL down cleanly; the VM powers off afterwards.
	OpStop Op = "stop"
)

// State is the guest's database lifecycle state.
type State string

const (
	StateNeedsBootstrap State = "needs_bootstrap"
	StateStarting       State = "starting"
	StateReady          State = "ready"
	StateStopping       State = "stopping"
	StateFailed         State = "failed"
)

// Stable error codes carried in Response.Error. They never contain
// PostgreSQL output or secrets.
const (
	ErrCodeInvalidRequest     = "invalid_request"
	ErrCodeAlreadyInitialized = "already_initialized"
	ErrCodeNotReady           = "not_ready"
	ErrCodeMajorMismatch      = "major_mismatch"
	ErrCodeInitFailed         = "init_failed"
	ErrCodeStartFailed        = "start_failed"
	ErrCodeVolumeFailed       = "volume_failed"
	ErrCodeAdminSQLFailed     = "admin_sql_failed"
)

// Request is one control request line.
type Request struct {
	Op Op `json:"op"`
	// SQL is the admin statement batch for OpAdminSQL. It can carry role
	// passwords and is never logged or echoed.
	SQL string `json:"sql,omitempty"`
}

// Response answers one control request.
type Response struct {
	State State  `json:"state"`
	Major int    `json:"pg_major,omitempty"`
	Error string `json:"error,omitempty"`
	// SQLState is the PostgreSQL error code of a failed admin request.
	SQLState string `json:"sqlstate,omitempty"`
}

// Validate rejects malformed requests before the guest acts on them.
func (r Request) Validate() error {
	switch r.Op {
	case OpStatus, OpStop, OpBootstrap:
		if r.SQL != "" {
			return fmt.Errorf("%s: unexpected sql", r.Op)
		}
		return nil
	case OpAdminSQL:
		if strings.TrimSpace(r.SQL) == "" || len(r.SQL) > MaxAdminSQLBytes {
			return errors.New("admin_sql: sql is empty or too large")
		}
		if strings.ContainsRune(r.SQL, 0) {
			return errors.New("admin_sql: sql contains NUL")
		}
		return nil
	default:
		return fmt.Errorf("unknown op %q", r.Op)
	}
}

// BootArgs returns the kernel command-line suffix that boots guest-init in
// database mode for the given major.
func BootArgs(major int) (string, error) {
	if major < MinMajor || major > MaxMajor {
		return "", fmt.Errorf("unsupported postgres major %d", major)
	}
	return ModeArg + " " + MajorArgKey + "=" + strconv.Itoa(major), nil
}

// ArchiveEnabled reports whether the kernel command line turns WAL
// archiving on.
func ArchiveEnabled(cmdline string) bool {
	for _, field := range strings.Fields(cmdline) {
		if field == ArchiveArg {
			return true
		}
	}
	return false
}

// ParseCmdline reports whether the kernel command line selects database
// mode and, if so, the PostgreSQL major it names. A database-mode line
// without a valid major is an error: guest-init must not guess.
func ParseCmdline(cmdline string) (database bool, major int, err error) {
	majorValue := ""
	for _, field := range strings.Fields(cmdline) {
		if field == ModeArg {
			database = true
		}
		if value, ok := strings.CutPrefix(field, MajorArgKey+"="); ok {
			majorValue = value
		}
	}
	if !database {
		return false, 0, nil
	}
	major, err = strconv.Atoi(majorValue)
	if err != nil || major < MinMajor || major > MaxMajor {
		return true, 0, fmt.Errorf("database mode needs %s in [%d,%d], got %q", MajorArgKey, MinMajor, MaxMajor, majorValue)
	}
	return true, major, nil
}
