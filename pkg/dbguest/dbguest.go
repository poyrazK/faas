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
	"time"
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
	// RestoreTargetArgKey boots an empty volume as a point-in-time restore:
	// guest-init pulls the restore source's base backup and recovers to the
	// target (RFC 3339, UTC), then promotes.
	RestoreTargetArgKey = "gregale.restore_target"
	// RestoreUntilEndArg lets a restore whose archive ends before its
	// target recover to the end of the archive instead of failing. schedd
	// sets it only when the source's node is lost, so the source could not
	// commit and archive past the target first.
	RestoreUntilEndArg = "gregale.restore_until_end=1"
	// SpareArg boots a spare (ADR-1027 wake latency): guest-init starts
	// everything but leaves the volume drive alone until OpAdopt, after
	// vmmd has pointed the drive at a tenant's volume.
	SpareArg = "gregale.spare=1"

	// MinMajor and MaxMajor bound the supported PostgreSQL majors.
	MinMajor = 14
	MaxMajor = 18

	// MaxRequestBytes bounds one control request line.
	MaxRequestBytes = 64 << 10
	// MaxResponseBytes bounds one control response line. The guest is
	// untrusted: a host reading without a bound could be made to buffer
	// without end.
	MaxResponseBytes = 64 << 10
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
	// OpBaseBackup streams a pg_basebackup to the host's archive
	// (ADR-1027) and returns its id in Response.Backup. It needs a ready
	// database with archiving on.
	OpBaseBackup Op = "basebackup"
	// OpFlushWAL makes the archive cover Request.After: once the guest
	// clock has passed it, the guest commits a transaction (a commit record
	// a point-in-time restore to After can stop at), switches WAL and waits
	// until that segment is archived. A restore needs it: recovery to a
	// time fails unless a commit after that time is archived.
	OpFlushWAL Op = "flush_wal"
	// OpAdopt makes a spare take the volume the host has just put behind
	// its volume drive: the guest drops what it cached of the drive,
	// mounts it and starts PostgreSQL. Accepted once, in StateSpare.
	OpAdopt Op = "adopt"
)

// MaxFlushAhead bounds how far in the future OpFlushWAL's After may be.
const MaxFlushAhead = 5 * time.Second

// State is the guest's database lifecycle state.
type State string

const (
	StateNeedsBootstrap State = "needs_bootstrap"
	// StateSpare: booted with no volume yet, waiting for OpAdopt.
	StateSpare    State = "spare"
	StateStarting State = "starting"
	StateReady    State = "ready"
	StateStopping State = "stopping"
	StateFailed   State = "failed"
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
	ErrCodeBackupFailed       = "backup_failed"
	ErrCodeArchiveOff         = "archive_off"
	ErrCodeRestoreFailed      = "restore_failed"
	ErrCodeFlushFailed        = "flush_failed"
	// ErrCodeNotSpare refuses OpAdopt outside StateSpare.
	ErrCodeNotSpare = "not_spare"
)

// Request is one control request line.
type Request struct {
	Op Op `json:"op"`
	// SQL is the admin statement batch for OpAdminSQL. It can carry role
	// passwords and is never logged or echoed.
	SQL string `json:"sql,omitempty"`
	// After is OpFlushWAL's instant.
	After time.Time `json:"after,omitzero"`
}

// Response answers one control request.
type Response struct {
	State State  `json:"state"`
	Major int    `json:"pg_major,omitempty"`
	Error string `json:"error,omitempty"`
	// SQLState is the PostgreSQL error code of a failed admin request.
	SQLState string `json:"sqlstate,omitempty"`
	// Backup is the id of the base backup OpBaseBackup stored.
	Backup string `json:"backup,omitempty"`
	// RecoveredTo is a restored database's last replayed commit: at or
	// before the target, and earlier when the archive ended first. Zero
	// when recovery replayed none: the restore is its base backup.
	RecoveredTo time.Time `json:"recovered_to,omitzero"`
}

// Validate rejects malformed requests before the guest acts on them.
func (r Request) Validate() error {
	switch r.Op {
	case OpStatus, OpStop, OpBootstrap, OpBaseBackup, OpAdopt:
		if r.SQL != "" || !r.After.IsZero() {
			return fmt.Errorf("%s: unexpected arguments", r.Op)
		}
		return nil
	case OpFlushWAL:
		if r.SQL != "" || r.After.IsZero() {
			return errors.New("flush_wal: needs only an instant")
		}
		return nil
	case OpAdminSQL:
		if strings.TrimSpace(r.SQL) == "" || len(r.SQL) > MaxAdminSQLBytes {
			return errors.New("admin_sql: sql is empty or too large")
		}
		if strings.ContainsRune(r.SQL, 0) {
			return errors.New("admin_sql: sql contains NUL")
		}
		if !r.After.IsZero() {
			return errors.New("admin_sql: unexpected instant")
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

// RestoreTargetArg renders the restore target boot argument.
func RestoreTargetArg(target time.Time) string {
	return RestoreTargetArgKey + "=" + target.UTC().Format(time.RFC3339Nano)
}

// RestoreTarget reports the point-in-time restore target on the kernel
// command line, if any.
func RestoreTarget(cmdline string) (time.Time, bool, error) {
	for _, field := range strings.Fields(cmdline) {
		if value, ok := strings.CutPrefix(field, RestoreTargetArgKey+"="); ok {
			t, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return time.Time{}, true, fmt.Errorf("invalid %s %q", RestoreTargetArgKey, value)
			}
			return t, true, nil
		}
	}
	return time.Time{}, false, nil
}

// ArchiveEnabled reports whether the kernel command line turns WAL
// archiving on.
func ArchiveEnabled(cmdline string) bool { return hasField(cmdline, ArchiveArg) }

// RestoreUntilEnd reports whether a restore may end at the end of the
// archive.
func RestoreUntilEnd(cmdline string) bool { return hasField(cmdline, RestoreUntilEndArg) }

// Spare reports whether the kernel command line boots a spare.
func Spare(cmdline string) bool { return hasField(cmdline, SpareArg) }

func hasField(cmdline, want string) bool {
	for _, field := range strings.Fields(cmdline) {
		if field == want {
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
