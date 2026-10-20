package dbarchive

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
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
	PruneBucket(ctx context.Context, databaseID string, windowSeconds int64) (PruneResult, error)
	// BackfillMirror queues copies of the database's bucket objects the
	// off-site mirror lacks; zero without a mirror.
	BackfillMirror(ctx context.Context, databaseID string) (int, error)
	LocalBytes(databaseID string) (int64, error)
	ArchiveBytes(ctx context.Context, databaseID string) (int64, error)
	PutHold(ctx context.Context, databaseID, name string, point time.Time) (Hold, error)
	GetHold(ctx context.Context, databaseID, name string) (Hold, error)
	DeleteHold(ctx context.Context, databaseID, name string) error
}

// Server answers relayed guest streams on dbbackupd's socket.
type Server struct {
	Store Store
	// Backups holds base backups; nil refuses backup ops.
	Backups BackupStore
	// Retention records windows and serves OpDrop; nil refuses OpDrop.
	Retention RetentionStore
	// Handoff serves OpSync and OpRelease; nil (no bucket) refuses them.
	Handoff Handoff
	Log     *slog.Logger
	now     func() time.Time
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
			if room, err := s.quotaRoom(header); err != nil || room < req.Size {
				s.refuseOverQuota(conn, header, err)
				return // the payload is unread; the stream is unusable
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

// handleAdmin serves vmmd's own stream: one admin op. Guest
// streams never reach it: the relay's header has no Admin.
func (s *Server) handleAdmin(ctx context.Context, conn net.Conn, br *bufio.Reader, header Header) {
	var req Request
	if err := ReadLine(br, &req); err != nil {
		return
	}
	if req.Op == OpSync || req.Op == OpRelease {
		s.handleHandoff(ctx, conn, header, req)
		return
	}
	if req.Op == OpHold || req.Op == OpGetHold || req.Op == OpUnhold {
		s.handleHold(ctx, conn, header, req)
		return
	}
	if s.Retention == nil || (req.Op != OpDrop && req.Op != OpPrune && req.Op != OpSize) || req.WindowSeconds < 0 {
		_ = WriteLine(conn, Response{Error: ErrCodeInvalid})
		return
	}
	if req.Op == OpSize {
		size, err := s.Retention.ArchiveBytes(ctx, header.DatabaseID)
		_ = WriteLine(conn, Response{Error: codeFor(err), Size: size})
		return
	}
	if req.Op == OpPrune {
		res, err := s.Retention.PruneBucket(ctx, header.DatabaseID, req.WindowSeconds)
		if err != nil {
			s.Log.Warn("dbarchive: bucket prune failed", "database_id", header.DatabaseID, "err", err)
		} else if res.Backups+res.WAL > 0 {
			s.Log.Info("dbarchive: bucket pruned", "database_id", header.DatabaseID, "backups", res.Backups, "wal", res.WAL)
		}
		if err == nil {
			// Only a lost node's databases are pruned in the bucket: no node
			// uploads what that node had not mirrored yet.
			s.backfillMirror(ctx, header.DatabaseID)
		}
		_ = WriteLine(conn, Response{Error: codeFor(err), Pruned: &res})
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

// backfillMirror queues the mirror's copies of the database's bucket
// archive. A failure is logged: the bucket prune succeeded, and the next
// sweep tries again.
func (s *Server) backfillMirror(ctx context.Context, databaseID string) {
	n, err := s.Retention.BackfillMirror(ctx, databaseID)
	switch {
	case err != nil:
		s.Log.Warn("dbarchive: mirror backfill failed", "database_id", databaseID, "err", err)
	case n > 0:
		s.Log.Info("dbarchive: mirror backfill queued", "database_id", databaseID, "files", n)
	}
}

// handleHold serves OpHold, OpGetHold and OpUnhold.
func (s *Server) handleHold(ctx context.Context, conn net.Conn, header Header, req Request) {
	if s.Retention == nil {
		_ = WriteLine(conn, Response{Error: ErrCodeInvalid})
		return
	}
	var hold Hold
	var err error
	switch req.Op {
	case OpHold:
		point, perr := time.Parse(time.RFC3339Nano, req.Target)
		if perr != nil || !ValidHoldName(req.Name) {
			_ = WriteLine(conn, Response{Error: ErrCodeInvalid})
			return
		}
		hold, err = s.Retention.PutHold(ctx, header.DatabaseID, req.Name, point)
		if err == nil {
			s.Log.Info("dbarchive: hold placed", "database_id", header.DatabaseID, "hold", req.Name, "point_in_time", hold.PointInTime)
		}
	case OpGetHold:
		hold, err = s.Retention.GetHold(ctx, header.DatabaseID, req.Name)
	default:
		err = s.Retention.DeleteHold(ctx, header.DatabaseID, req.Name)
		if err == nil {
			s.Log.Info("dbarchive: hold removed", "database_id", header.DatabaseID, "hold", req.Name)
		}
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		s.Log.Warn("dbarchive: hold op failed", "database_id", header.DatabaseID, "op", req.Op, "err", err)
	}
	resp := Response{Error: codeFor(err)}
	if err == nil && req.Op != OpUnhold {
		resp.Hold = &hold
	}
	_ = WriteLine(conn, resp)
}

// handleHandoff serves OpSync and OpRelease.
func (s *Server) handleHandoff(ctx context.Context, conn net.Conn, header Header, req Request) {
	if s.Handoff == nil {
		_ = WriteLine(conn, Response{Error: ErrCodeInvalid})
		return
	}
	if req.Op == OpSync {
		n, err := s.Handoff.SyncDatabase(ctx, header.DatabaseID)
		if err != nil {
			s.Log.Warn("dbarchive: archive sync failed", "database_id", header.DatabaseID, "err", err)
		}
		_ = WriteLine(conn, Response{Error: codeFor(err), Size: int64(n)})
		return
	}
	err := s.Handoff.Release(ctx, header.DatabaseID)
	if err != nil {
		s.Log.Warn("dbarchive: archive release failed", "database_id", header.DatabaseID, "err", err)
	} else {
		s.Log.Info("dbarchive: archive released to the bucket", "database_id", header.DatabaseID)
	}
	_ = WriteLine(conn, Response{Error: codeFor(err)})
}

// DropArchive asks dbbackupd at socketPath to delete a deleted database's
// archive. vmmd calls it; the admin header is what lets it through.
func DropArchive(ctx context.Context, socketPath, databaseID string) error {
	_, err := admin(ctx, socketPath, databaseID, Request{Op: OpDrop})
	return err
}

// PruneArchive asks dbbackupd at socketPath to prune a database's archive
// in the bucket to its restore window.
func PruneArchive(ctx context.Context, socketPath, databaseID string, windowSeconds int64) (PruneResult, error) {
	resp, err := admin(ctx, socketPath, databaseID, Request{Op: OpPrune, WindowSeconds: windowSeconds})
	if err != nil || resp.Pruned == nil {
		return PruneResult{}, err
	}
	return *resp.Pruned, nil
}

// ArchiveBytes asks dbbackupd at socketPath for the size of a database's
// whole archive: on its node and in the bucket, each file once.
func ArchiveBytes(ctx context.Context, socketPath, databaseID string) (int64, error) {
	resp, err := admin(ctx, socketPath, databaseID, Request{Op: OpSize})
	return resp.Size, err
}

// PutArchiveHold asks dbbackupd at socketPath to pin point in a
// database's archive under name.
func PutArchiveHold(ctx context.Context, socketPath, databaseID, name string, point time.Time) (Hold, error) {
	return holdOp(ctx, socketPath, databaseID, Request{Op: OpHold, Name: name, Target: point.UTC().Format(time.RFC3339Nano)})
}

// GetArchiveHold asks dbbackupd at socketPath for a hold; ErrNotFound when
// there is none.
func GetArchiveHold(ctx context.Context, socketPath, databaseID, name string) (Hold, error) {
	return holdOp(ctx, socketPath, databaseID, Request{Op: OpGetHold, Name: name})
}

// DeleteArchiveHold asks dbbackupd at socketPath to remove a hold.
func DeleteArchiveHold(ctx context.Context, socketPath, databaseID, name string) error {
	_, err := admin(ctx, socketPath, databaseID, Request{Op: OpUnhold, Name: name})
	return err
}

func holdOp(ctx context.Context, socketPath, databaseID string, req Request) (Hold, error) {
	resp, err := admin(ctx, socketPath, databaseID, req)
	if err != nil {
		return Hold{}, err
	}
	if resp.Hold == nil {
		return Hold{}, errors.New("dbarchive: hold response without a hold")
	}
	return *resp.Hold, nil
}

func admin(ctx context.Context, socketPath, databaseID string, req Request) (Response, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return Response{}, fmt.Errorf("dbarchive: dial %s: %w", socketPath, err)
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := WriteLine(conn, Header{DatabaseID: databaseID, Admin: true}); err != nil {
		return Response{}, err
	}
	if err := WriteLine(conn, req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := ReadLine(bufio.NewReader(conn), &resp); err != nil {
		return Response{}, err
	}
	return resp, errorFor(resp.Error)
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
		room, err := s.quotaRoom(header)
		if err != nil || room <= 0 {
			s.refuseOverQuota(conn, header, err)
			return false
		}
		body := &quotaReader{r: &chunkReader{r: br}, room: room}
		m, err := s.Backups.PutBackup(ctx, header.DatabaseID, body, now())
		if errors.Is(err, ErrQuotaExceeded) || body.over {
			s.refuseOverQuota(conn, header, nil)
			return false
		}
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

// quotaRoom is how many more bytes the stream's database may archive here:
// unlimited without a quota (or without the archive's size to check).
func (s *Server) quotaRoom(header Header) (int64, error) {
	if header.QuotaBytes == 0 || s.Retention == nil {
		return math.MaxInt64, nil
	}
	used, err := s.Retention.LocalBytes(header.DatabaseID)
	if err != nil {
		return 0, err
	}
	return header.QuotaBytes - used, nil
}

func (s *Server) refuseOverQuota(conn net.Conn, header Header, err error) {
	if err != nil {
		s.Log.Warn("dbarchive: size the archive for its quota", "database_id", header.DatabaseID, "err", err)
		_ = WriteLine(conn, Response{Error: ErrCodeInternal})
		return
	}
	s.Log.Warn("dbarchive: archive quota exceeded; refusing", "database_id", header.DatabaseID, "quota_bytes", header.QuotaBytes)
	_ = WriteLine(conn, Response{Error: ErrCodeQuota})
}

// quotaReader fails once more than room bytes have been read.
type quotaReader struct {
	r    io.Reader
	room int64
	over bool
}

func (q *quotaReader) Read(p []byte) (int, error) {
	n, err := q.r.Read(p)
	q.room -= int64(n)
	if q.room < 0 {
		q.over = true
		return n, ErrQuotaExceeded
	}
	return n, err
}
