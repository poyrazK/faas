package dbarchive

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

// BackupStore holds base backups.
type BackupStore interface {
	PutBackup(ctx context.Context, databaseID string, r io.Reader, now time.Time) (BackupManifest, error)
	SelectBackup(ctx context.Context, wal Store, databaseID string, target time.Time) (BackupManifest, error)
	OpenBackup(ctx context.Context, databaseID, id string) (io.ReadCloser, error)
}

// RetentionStore records restore windows and drops deleted databases'
// archives.
type RetentionStore interface {
	NoteWindow(databaseID string, seconds int64) error
	Drop(ctx context.Context, databaseID string) error
}

// Server answers relayed guest streams on dbbackupd's socket.
type Server struct {
	Store Store
	// Backups holds base backups; nil refuses backup ops.
	Backups BackupStore
	// Retention records windows and serves OpDrop; nil refuses OpDrop.
	Retention RetentionStore
	Log       *slog.Logger
	now       func() time.Time
}

// Serve accepts streams until ctx ends or ln fails.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	if s.Log == nil {
		s.Log = slog.Default()
	}
	stop := context.AfterFunc(ctx, func() { _ = ln.Close() })
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { _ = conn.Close() }()
			s.handle(ctx, conn)
		}()
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	br := bufio.NewReader(conn)
	var header Header
	if err := ReadLine(br, &header); err != nil || header.Validate() != nil {
		s.Log.Warn("dbarchive: refused stream without a valid header", "err", err)
		return
	}
	if header.Admin {
		s.handleAdmin(ctx, conn, br, header)
		return
	}
	if s.Retention != nil && header.RestoreWindowSeconds > 0 {
		if err := s.Retention.NoteWindow(header.DatabaseID, header.RestoreWindowSeconds); err != nil {
			s.Log.Warn("dbarchive: record restore window", "database_id", header.DatabaseID, "err", err)
		}
	}
	for {
		var req Request
		if err := ReadLine(br, &req); err != nil {
			if !errors.Is(err, io.EOF) {
				s.Log.Warn("dbarchive: bad request", "database_id", header.DatabaseID, "err", err)
			}
			return
		}
		if (req.Op == OpPutWAL || req.Op == OpGetWAL) && !ValidWALName(req.Name) {
			_ = WriteLine(conn, Response{Error: ErrCodeInvalid})
			return
		}
		switch req.Op {
		case OpPutBackup, OpGetBackup:
			if !s.handleBackup(ctx, conn, br, header, req) {
				return
			}
		case OpPutWAL:
			if req.Size <= 0 || req.Size > MaxWALBytes {
				_ = WriteLine(conn, Response{Error: ErrCodeInvalid})
				return
			}
			err := s.Store.PutWAL(ctx, header.DatabaseID, req.Name, io.LimitReader(br, req.Size), req.Size)
			if err != nil {
				s.Log.Warn("dbarchive: archive failed", "database_id", header.DatabaseID, "name", req.Name, "err", err)
			}
			if WriteLine(conn, Response{Error: codeFor(err)}) != nil || (err != nil && !errors.Is(err, ErrConflict)) {
				// The payload may be partly unread; the stream is unusable.
				return
			}
		case OpGetWAL:
			source := header.DatabaseID
			if header.RestoreSource != "" {
				source = header.RestoreSource
			}
			rc, size, err := s.Store.OpenWAL(ctx, source, req.Name)
			if err != nil {
				if WriteLine(conn, Response{Error: codeFor(err)}) != nil {
					return
				}
				continue
			}
			err = WriteLine(conn, Response{Size: size})
			if err == nil {
				_, err = io.CopyN(conn, rc, size)
			}
			_ = rc.Close()
			if err != nil {
				return
			}
		default:
			_ = WriteLine(conn, Response{Error: ErrCodeInvalid})
			return
		}
	}
}

// handleAdmin serves vmmd's own stream: one OpDrop. Guest streams never
// reach it: the relay's header has no Admin.
func (s *Server) handleAdmin(ctx context.Context, conn net.Conn, br *bufio.Reader, header Header) {
	var req Request
	if err := ReadLine(br, &req); err != nil {
		return
	}
	if req.Op != OpDrop || s.Retention == nil {
		_ = WriteLine(conn, Response{Error: ErrCodeInvalid})
		return
	}
	err := s.Retention.Drop(ctx, header.DatabaseID)
	if err != nil {
		s.Log.Warn("dbarchive: drop failed", "database_id", header.DatabaseID, "err", err)
	} else {
		s.Log.Info("dbarchive: archive dropped", "database_id", header.DatabaseID)
	}
	_ = WriteLine(conn, Response{Error: codeFor(err)})
}

// DropArchive asks dbbackupd at socketPath to delete a deleted database's
// archive. vmmd calls it; the admin header is what lets it through.
func DropArchive(ctx context.Context, socketPath, databaseID string) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return fmt.Errorf("dbarchive: dial %s: %w", socketPath, err)
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := WriteLine(conn, Header{DatabaseID: databaseID, Admin: true}); err != nil {
		return err
	}
	if err := WriteLine(conn, Request{Op: OpDrop}); err != nil {
		return err
	}
	var resp Response
	if err := ReadLine(bufio.NewReader(conn), &resp); err != nil {
		return err
	}
	return errorFor(resp.Error)
}

// handleBackup serves one backup op and reports whether the stream is still
// usable.
func (s *Server) handleBackup(ctx context.Context, conn net.Conn, br *bufio.Reader, header Header, req Request) bool {
	if s.Backups == nil {
		_ = WriteLine(conn, Response{Error: ErrCodeInvalid})
		return false
	}
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	if req.Op == OpPutBackup {
		body := &chunkReader{r: br}
		m, err := s.Backups.PutBackup(ctx, header.DatabaseID, body, now())
		if err != nil {
			s.Log.Warn("dbarchive: base backup failed", "database_id", header.DatabaseID, "err", err)
			_ = WriteLine(conn, Response{Error: codeFor(err)})
			return false
		}
		s.Log.Info("dbarchive: base backup stored", "database_id", header.DatabaseID, "backup", m.ID, "bytes", m.Bytes)
		return WriteLine(conn, Response{Name: m.ID}) == nil
	}
	source := header.DatabaseID
	if header.RestoreSource != "" {
		source = header.RestoreSource
	}
	target, err := time.Parse(time.RFC3339Nano, req.Target)
	if err != nil {
		_ = WriteLine(conn, Response{Error: ErrCodeInvalid})
		return false
	}
	m, err := s.Backups.SelectBackup(ctx, s.Store, source, target)
	if err != nil {
		return WriteLine(conn, Response{Error: codeFor(err)}) == nil
	}
	rc, err := s.Backups.OpenBackup(ctx, source, m.ID)
	if err != nil {
		return WriteLine(conn, Response{Error: codeFor(err)}) == nil
	}
	defer func() { _ = rc.Close() }()
	if WriteLine(conn, Response{Name: m.ID}) != nil {
		return false
	}
	bw := bufio.NewWriterSize(conn, 256<<10)
	if _, err := writeChunks(bw, rc); err != nil {
		return false
	}
	return bw.Flush() == nil
}
