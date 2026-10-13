package dbarchive

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
)

// Server answers relayed guest streams on dbbackupd's socket.
type Server struct {
	Store Store
	Log   *slog.Logger
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
	for {
		var req Request
		if err := ReadLine(br, &req); err != nil {
			if !errors.Is(err, io.EOF) {
				s.Log.Warn("dbarchive: bad request", "database_id", header.DatabaseID, "err", err)
			}
			return
		}
		if !ValidWALName(req.Name) {
			_ = WriteLine(conn, Response{Error: ErrCodeInvalid})
			return
		}
		switch req.Op {
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
