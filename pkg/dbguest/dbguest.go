// Package dbguest is the host-guest contract for database microVMs
// (ADR-1027): the kernel command-line keys that select guest-init's database
// mode, the vsock ports it serves, and its control protocol. The package has
// no I/O so guest-init and the host side share one definition.
package dbguest

import (
	"errors"
	"fmt"
	"regexp"
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
	// ReplicationPort carries streaming replication between a production
	// database's primary and its standby (ADR-1027 production class). A
	// primary's guest-init serves it and admits each stream as the
	// replication role only; a standby's guest-init dials it on the host,
	// which relays the stream to the primary's node.
	ReplicationPort uint32 = 5435
	// StandbyArg boots the volume as a hot standby of the database's
	// primary, streaming through the host relay. An empty volume is first
	// seeded with a base backup of the primary over the same relay.
	StandbyArg = "gregale.standby=1"
	// PromoteArg, with StandbyArg, promotes the standby as soon as it
	// replays: a standby that took over while parked (a failover). It
	// boots without a primary.
	PromoteArg = "gregale.promote=1"
	// ImportPort is the host vsock port an import's source connections
	// reach (OpImport). The host relays each to the one source endpoint
	// the control plane named, over TLS it verifies itself; the guest
	// speaks plaintext to it.
	ImportPort uint32 = 5436
	// UpgradeFromArgKey boots a cluster of an older major for an in-place
	// major upgrade (pg_upgrade --link) to the major MajorArgKey names.
	// The old major's base is the VM's third drive (UpgradeBaseDevice),
	// whose binaries pg_upgrade runs.
	UpgradeFromArgKey = "gregale.upgrade_from"

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
	// OpReplication reports a primary's view of its standby. With
	// Request.Replication set it first changes it: async makes sure the
	// replication role and the standby's slot exist, sync also makes
	// every commit wait until the standby has flushed it, off stops
	// waiting and drops the slot. Sync lasts until the next boot: every
	// boot starts asynchronous.
	OpReplication Op = "replication"
	// OpPromote ends a standby's recovery: it becomes a primary on a new
	// timeline and reports ready. Accepted in StateStandby.
	OpPromote Op = "promote"
	// OpImport copies another PostgreSQL database (Request.Import) into an
	// empty database here, in the background: a consistent pg_dump of the
	// source through the host's import relay, restored in one transaction
	// as the target's owner role. A running or finished import is
	// reported, not restarted. Accepted in StateReady.
	OpImport Op = "import"
	// OpImportStatus reports the import, if any.
	OpImportStatus Op = "import_status"
	// OpMarkDrill commits a restore drill's mark (Request.Drill) on a
	// ready primary, in a table the superuser owns in the maintenance
	// database, where no customer role reaches it. The mark replaces the
	// previous one. Response.MarkedAt is the guest clock once it
	// committed: a point-in-time restore to that instant holds the mark.
	OpMarkDrill Op = "mark_drill"
	// OpCheckDrill reports ErrCodeDrillMarkMissing unless the database
	// holds Request.Drill's mark: a restored copy holds it only if
	// recovery replayed the source's WAL up to the mark's commit.
	OpCheckDrill Op = "check_drill"
)

// ImportSource names the database to copy and how to log in to it. The
// host relay reaches the source's network endpoint; the guest never learns
// it. Password is sensitive: never logged or echoed.
type ImportSource struct {
	Database string `json:"database"`
	User     string `json:"user"`
	Password string `json:"password"`
	// TargetDatabase receives the copy and must hold no user objects;
	// Owner is the role that owns everything restored (the target's
	// schema owner). SkipExtensions are source extensions not to copy
	// (the source provider's own).
	TargetDatabase string   `json:"target_database"`
	Owner          string   `json:"owner"`
	SkipExtensions []string `json:"skip_extensions,omitempty"`
	// OperationID names the import (a lowercase UUID: a cutover's
	// copy_operation_id, ADR-1370). Importing again under it reports the
	// import unchanged, or restarts a failed one; another ID while one is
	// running or done is ErrCodeImportOperationMismatch. A finished copy
	// records it in the target database inside the restore transaction,
	// so it outlives the VM.
	OperationID string `json:"operation_id"`
}

// ImportState is an import's progress.
type ImportState string

const (
	ImportNone    ImportState = "none"
	ImportRunning ImportState = "running"
	ImportDone    ImportState = "done"
	ImportFailed  ImportState = "failed"
)

// ImportStatus reports an import. Error is a stable code.
type ImportStatus struct {
	State      ImportState `json:"state"`
	Error      string      `json:"error,omitempty"`
	StartedAt  time.Time   `json:"started_at,omitzero"`
	FinishedAt time.Time   `json:"finished_at,omitzero"`
	// SourceMajor is the source server's PostgreSQL major.
	SourceMajor int `json:"source_major,omitempty"`
	// OperationID is the import this status describes.
	OperationID string `json:"operation_id,omitempty"`
	// MissingExtensions names the source extensions the target cannot
	// create, for extension_unavailable (ReportableExtensions).
	MissingExtensions []string `json:"missing_extensions,omitempty"`
	// RequiredVolumeBytes is, for target_too_small, the volume size the
	// import needs. Zero when free space ran out during the copy, which
	// the estimate did not foresee.
	RequiredVolumeBytes int64 `json:"required_volume_bytes,omitempty"`
}

// maxReportedExtensions bounds MissingExtensions.
const maxReportedExtensions = 16

// ReportableExtensions keeps the names in names that are plain
// identifiers, at most maxReportedExtensions of them. The names come from
// the source server, so anything else (connection data included) never
// leaves the guest, and the host filters again what a guest reports.
func ReportableExtensions(names []string) []string {
	var out []string
	for _, name := range names {
		if len(out) == maxReportedExtensions {
			break
		}
		if identifierPattern.MatchString(name) {
			out = append(out, name)
		}
	}
	return out
}

// Import error codes.
const (
	ErrCodeImportFailed   = "import_failed"
	ErrCodeSourceNewer    = "source_newer"
	ErrCodeSourceDown     = "source_unreachable"
	ErrCodeExtension      = "extension_unavailable"
	ErrCodeTargetNotEmpty = "target_not_empty"
	// ErrCodeTargetTooSmall: the target volume cannot hold the source's
	// copy and its dump; RequiredVolumeBytes says how large it must be.
	ErrCodeTargetTooSmall = "target_too_small"
	// ErrCodeImportOperationMismatch: another import is running, or done,
	// in this database.
	ErrCodeImportOperationMismatch = "import_operation_mismatch"
)

// operationIDPattern is a lowercase UUID.
var operationIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidOperationID reports whether id names an import.
func ValidOperationID(id string) bool { return operationIDPattern.MatchString(id) }

// ReplicationMode is how a primary treats its standby.
type ReplicationMode string

const (
	ReplicationOff   ReplicationMode = "off"
	ReplicationAsync ReplicationMode = "async"
	ReplicationSync  ReplicationMode = "sync"
)

// ReplicationStatus is a primary's view of its standby.
type ReplicationStatus struct {
	// Mode is what the primary runs.
	Mode ReplicationMode `json:"mode"`
	// Streaming: the standby is connected and streaming.
	Streaming bool `json:"streaming,omitempty"`
	// Synchronous: commits wait for the standby, which has flushed every
	// one acknowledged so far.
	Synchronous bool `json:"synchronous,omitempty"`
	// LagBytes is how far the standby's flushed WAL trails the primary's.
	LagBytes int64 `json:"lag_bytes,omitempty"`
}

// MaxFlushAhead bounds how far in the future OpFlushWAL's After may be.
const MaxFlushAhead = 5 * time.Second

// State is the guest's database lifecycle state.
type State string

const (
	StateNeedsBootstrap State = "needs_bootstrap"
	// StateSpare: booted with no volume yet, waiting for OpAdopt.
	StateSpare State = "spare"
	// StateStandby: a hot standby replaying from its primary; it serves
	// no sessions until promoted.
	StateStandby  State = "standby"
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
	// ErrCodeNotStandby refuses OpPromote outside StateStandby.
	ErrCodeNotStandby        = "not_standby"
	ErrCodeReplicationFailed = "replication_failed"
	ErrCodePromoteFailed     = "promote_failed"
	// ErrCodeSeedFailed: a standby could not copy its primary.
	ErrCodeSeedFailed = "seed_failed"
	// ErrCodeUpgradeRefused: pg_upgrade refused the cluster before
	// changing it (its check, or a failure before linking); the cluster is
	// intact on its old major.
	ErrCodeUpgradeRefused = "upgrade_refused"
	// ErrCodeUpgradeFailed: the upgrade failed or was interrupted after
	// pg_upgrade linked the cluster's files, so the old cluster cannot
	// start again; the database needs a restore from its backups.
	ErrCodeUpgradeFailed = "upgrade_failed"
	// ErrCodeDrillMarkFailed: the drill's mark could not be committed.
	ErrCodeDrillMarkFailed = "drill_mark_failed"
	// ErrCodeDrillMarkMissing: the database lacks the drill's mark.
	ErrCodeDrillMarkMissing = "drill_mark_missing"
)

// Request is one control request line.
type Request struct {
	Op Op `json:"op"`
	// SQL is the admin statement batch for OpAdminSQL. It can carry role
	// passwords and is never logged or echoed.
	SQL string `json:"sql,omitempty"`
	// After is OpFlushWAL's instant.
	After time.Time `json:"after,omitzero"`
	// Replication is the mode OpReplication sets; empty only reports.
	Replication ReplicationMode `json:"replication,omitempty"`
	// Import is OpImport's source.
	Import *ImportSource `json:"import,omitempty"`
	// Drill is the restore drill OpMarkDrill marks and OpCheckDrill looks
	// for: its id, a lowercase UUID.
	Drill string `json:"drill,omitempty"`
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
	// Replication answers OpReplication.
	Replication *ReplicationStatus `json:"replication,omitempty"`
	// Import answers OpImport and OpImportStatus.
	Import *ImportStatus `json:"import,omitempty"`
	// Storage is the volume's use, on a ready database or a standby.
	Storage *StorageStatus `json:"storage,omitempty"`
	// MarkedAt answers OpMarkDrill.
	MarkedAt time.Time `json:"marked_at,omitzero"`
	// Stale is set by the host, never the guest: the VM runs an outdated
	// base or launch, which a restart replaces.
	Stale bool `json:"-"`
}

// StorageStatus reports a database's volume. ReadOnly is the storage
// guard's read-only mode (a primary short of space); Low is space below
// the level at which the guard would turn it writable again.
type StorageStatus struct {
	TotalBytes int64 `json:"total_bytes"`
	FreeBytes  int64 `json:"free_bytes"`
	ReadOnly   bool  `json:"read_only,omitempty"`
	Low        bool  `json:"low,omitempty"`
}

// Validate rejects malformed requests before the guest acts on them.
func (r Request) Validate() error {
	if r.Replication != "" && r.Op != OpReplication {
		return fmt.Errorf("%s: unexpected replication mode", r.Op)
	}
	if (r.Import != nil) != (r.Op == OpImport) {
		return fmt.Errorf("%s: an import source goes with import only", r.Op)
	}
	if drill := r.Op == OpMarkDrill || r.Op == OpCheckDrill; drill != (r.Drill != "") {
		return fmt.Errorf("%s: a drill goes with mark_drill and check_drill only", r.Op)
	}
	switch r.Op {
	case OpMarkDrill, OpCheckDrill:
		if r.SQL != "" || !r.After.IsZero() || !operationIDPattern.MatchString(r.Drill) {
			return fmt.Errorf("%s: needs only a drill id", r.Op)
		}
		return nil
	case OpImport:
		if r.SQL != "" || !r.After.IsZero() {
			return errors.New("import: unexpected arguments")
		}
		return r.Import.Validate()
	case OpReplication:
		switch r.Replication {
		case "", ReplicationOff, ReplicationAsync, ReplicationSync:
		default:
			return fmt.Errorf("replication: unknown mode %q", r.Replication)
		}
		if r.SQL != "" || !r.After.IsZero() {
			return errors.New("replication: unexpected arguments")
		}
		return nil
	case OpStatus, OpStop, OpBootstrap, OpBaseBackup, OpAdopt, OpPromote, OpImportStatus:
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

// UpgradeFromArg renders the upgrade boot argument.
func UpgradeFromArg(major int) string { return UpgradeFromArgKey + "=" + strconv.Itoa(major) }

// UpgradeFrom reports the major an upgrade boot upgrades from (0: none).
// It must be an older supported major than to.
func UpgradeFrom(cmdline string, to int) (int, error) {
	for _, field := range strings.Fields(cmdline) {
		if value, ok := strings.CutPrefix(field, UpgradeFromArgKey+"="); ok {
			from, err := strconv.Atoi(value)
			if err != nil || from < MinMajor || from >= to {
				return 0, fmt.Errorf("invalid %s %q for major %d", UpgradeFromArgKey, value, to)
			}
			return from, nil
		}
	}
	return 0, nil
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

// Standby reports whether the kernel command line boots a standby.
func Standby(cmdline string) bool { return hasField(cmdline, StandbyArg) }

// Promote reports whether the kernel command line promotes a standby at
// boot.
func Promote(cmdline string) bool { return hasField(cmdline, PromoteArg) }

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

// identifierPattern is a plain lowercase PostgreSQL identifier: the only
// names an import passes to its tools.
var identifierPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// Validate rejects a source that names anything but plain identifiers, so
// nothing but the password reaches a command line or connection string
// unquoted, and the password only through the environment.
func (s *ImportSource) Validate() error {
	switch {
	case s == nil:
		return errors.New("import: no source")
	case !identifierPattern.MatchString(s.Database) || !identifierPattern.MatchString(s.TargetDatabase) || s.TargetDatabase == "postgres":
		return errors.New("import: invalid database name")
	case !identifierPattern.MatchString(s.User) || !identifierPattern.MatchString(s.Owner) || s.Owner == "postgres":
		return errors.New("import: invalid role name")
	case s.Password == "" || len(s.Password) > 1024 || strings.ContainsAny(s.Password, "\x00\n"):
		return errors.New("import: invalid password")
	case len(s.SkipExtensions) > 32:
		return errors.New("import: too many skipped extensions")
	case !ValidOperationID(s.OperationID):
		return errors.New("import: invalid operation id")
	}
	for _, ext := range s.SkipExtensions {
		if !identifierPattern.MatchString(ext) {
			return errors.New("import: invalid extension name")
		}
	}
	return nil
}
