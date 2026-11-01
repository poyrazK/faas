// Package dbarchive carries native managed databases' WAL and base backups
// off their VMs (ADR-1027). A database VM has no network: guest-init speaks
// this protocol over vsock, vmmd relays each stream to dbbackupd after
// stamping the database id it booted (the guest cannot choose it), and
// dbbackupd stores the bytes.
//
// Wire format, per request on one stream:
//
//	request line (JSON, newline-terminated) [payload bytes]
//	response line (JSON, newline-terminated) [payload bytes]
//
// The relay's Header line precedes the guest's first request.
package dbarchive

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
)

// Ops a guest may request.
const (
	// OpPutWAL archives one WAL file: Size payload bytes follow.
	OpPutWAL = "put_wal"
	// OpGetWAL fetches one archived WAL file (restore_command). It reads the
	// restore source's archive when the relay names one.
	OpGetWAL = "get_wal"
	// OpDrop deletes the database's whole archive. Only vmmd's own admin
	// streams may send it, never a relayed guest stream.
	OpDrop = "drop"
	// OpPrune prunes the database's archive in the bucket to
	// Request.WindowSeconds (see TieredStore.PruneBucket). Admin only.
	OpPrune = "prune"
	// OpSize reports the bytes of the database's archive on this node
	// (Response.Size), for the restore-history meter. Admin only.
	OpSize = "size"
	// OpSync ships the database's pending archive files to the bucket now;
	// Response.Size counts them. Admin only (handoff.go).
	OpSync = "sync"
	// OpRelease ships them and then drops this node's copy of the archive,
	// leaving the bucket's. Admin only.
	OpRelease = "release"
	// OpHold pins Request.Target in the database's archive under
	// Request.Name (holds.go); Response.Hold is the hold. Admin only.
	OpHold = "hold"
	// OpGetHold returns the hold Request.Name. Admin only.
	OpGetHold = "get_hold"
	// OpUnhold removes the hold Request.Name. Admin only.
	OpUnhold = "unhold"
)

// Stable error codes in Response.Error.
const (
	ErrCodeNotFound = "not_found"
	ErrCodeConflict = "conflict"
	// ErrCodeQuota refuses archive bytes past Header.QuotaBytes.
	ErrCodeQuota    = "quota_exceeded"
	ErrCodeInvalid  = "invalid"
	ErrCodeInternal = "internal"
)

// MaxLineBytes bounds a request, response or header line.
const MaxLineBytes = 4 << 10

// MaxWALBytes bounds one WAL file: PostgreSQL's largest configurable
// segment is 1 GiB.
const MaxWALBytes = 1 << 30

// ErrNotFound reports a missing archive object.
var ErrNotFound = errors.New("dbarchive: not found")

// ErrConflict reports an archive object that already exists with other
// content. PostgreSQL must never overwrite archived WAL.
var ErrConflict = errors.New("dbarchive: object exists with different content")

// ErrQuotaExceeded refuses archive bytes past the stream's quota.
var ErrQuotaExceeded = errors.New("dbarchive: archive quota exceeded")

// walName matches the files archive_command ships: segments, partial
// segments, backup history and timeline history files.
var walName = regexp.MustCompile(`^([0-9A-F]{24}(\.partial|\.[0-9A-F]{8}\.backup)?|[0-9A-F]{8}\.history)$`)

// ValidWALName reports whether name is a WAL file name.
func ValidWALName(name string) bool { return walName.MatchString(name) }

// databaseID matches the lower-case UUIDs database ids are.
var databaseID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidDatabaseID reports whether id is a database id.
func ValidDatabaseID(id string) bool { return databaseID.MatchString(id) }

// Header is written by vmmd before a stream: its relay writes it ahead of
// every guest stream, so the guest controls none of it.
type Header struct {
	DatabaseID string `json:"database_id"`
	// RestoreSource is the database whose archive OpGetWAL reads while this
	// one recovers from it; empty means its own archive.
	RestoreSource string `json:"restore_source,omitempty"`
	// RestoreWindowSeconds is the database's restore window, which sets
	// how long its archive is kept (see retention.go).
	RestoreWindowSeconds int64 `json:"restore_window_seconds,omitempty"`
	// Admin marks vmmd's own stream for OpDrop and OpPrune. The relay
	// never sets it.
	Admin bool `json:"admin,omitempty"`
	// QuotaBytes caps the database's archive on this node; zero is
	// unlimited. A guest can stream WAL and backups at will, past the
	// volume's rate limiter, so vmmd stamps a quota on every relayed
	// stream and one database cannot fill the node's archive disk.
	QuotaBytes int64 `json:"quota_bytes,omitempty"`
}

// Validate rejects a header naming anything but database ids.
func (h Header) Validate() error {
	if !ValidDatabaseID(h.DatabaseID) || (h.RestoreSource != "" && !ValidDatabaseID(h.RestoreSource)) || h.RestoreWindowSeconds < 0 || h.QuotaBytes < 0 {
		return errors.New("dbarchive: invalid header")
	}
	if h.Admin && (h.RestoreSource != "" || h.RestoreWindowSeconds != 0 || h.QuotaBytes != 0) {
		return errors.New("dbarchive: invalid admin header")
	}
	return nil
}

// Request is one guest request.
type Request struct {
	Op   string `json:"op"`
	Name string `json:"name,omitempty"`
	Size int64  `json:"size,omitempty"`
	// Target is OpGetBackup's recovery target time and OpHold's point
	// (RFC 3339).
	Target string `json:"target,omitempty"`
	// WindowSeconds is OpPrune's restore window.
	WindowSeconds int64 `json:"window_seconds,omitempty"`
}

// Response answers one request. For OpGetWAL, Size payload bytes follow a
// response without Error.
type Response struct {
	Error string `json:"error,omitempty"`
	Size  int64  `json:"size,omitempty"`
	// Name is the backup id of OpPutBackup and OpGetBackup.
	Name string `json:"name,omitempty"`
	// Pruned is what OpPrune removed.
	Pruned *PruneResult `json:"pruned,omitempty"`
	// Hold answers OpHold and OpGetHold.
	Hold *Hold `json:"hold,omitempty"`
}

// WriteLine writes v as one JSON line.
func WriteLine(w io.Writer, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(raw) >= MaxLineBytes {
		return errors.New("dbarchive: line too long")
	}
	_, err = w.Write(append(raw, '\n'))
	return err
}

// ReadLine reads one bounded JSON line into v.
func ReadLine(r *bufio.Reader, v any) error {
	var line []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return err
		}
		line = append(line, chunk...)
		if len(line) >= MaxLineBytes {
			return errors.New("dbarchive: line too long")
		}
		if !isPrefix {
			break
		}
	}
	if err := json.Unmarshal(line, v); err != nil {
		return fmt.Errorf("dbarchive: malformed line: %w", err)
	}
	return nil
}

// errorFor maps a response code to the package errors.
func errorFor(code string) error {
	switch code {
	case "":
		return nil
	case ErrCodeNotFound:
		return ErrNotFound
	case ErrCodeConflict:
		return ErrConflict
	case ErrCodeQuota:
		return ErrQuotaExceeded
	default:
		return fmt.Errorf("dbarchive: %s", code)
	}
}

// codeFor maps an error to its response code.
func codeFor(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrNotFound):
		return ErrCodeNotFound
	case errors.Is(err, ErrConflict):
		return ErrCodeConflict
	case errors.Is(err, ErrQuotaExceeded):
		return ErrCodeQuota
	default:
		return ErrCodeInternal
	}
}
