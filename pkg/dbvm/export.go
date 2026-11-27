package dbvm

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/pgexport"
)

func (r *Runtime) ExportDatabase(ctx context.Context, id, database string, major int, dst io.Writer) (pgexport.Source, error) {
	if !pgexport.ValidDatabase(database) || major < dbguest.MinMajor || major > dbguest.MaxMajor {
		return pgexport.Source{}, pgexport.ErrIncomplete
	}
	vm, err := r.running(id)
	if err != nil {
		return pgexport.Source{}, err
	}
	// The guest is already ready. An older guest has no export listener.
	conn, err := dialOnce(ctx, r.udsPath(vm), dbguest.ExportPort)
	if err != nil {
		return pgexport.Source{}, err
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline := time.Now().Add(pgexport.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	if err := json.NewEncoder(conn).Encode(database); err != nil {
		return pgexport.Source{}, err
	}
	source, err := pgexport.ReadStream(conn, dst)
	if err == nil && source.Major != major {
		err = pgexport.ErrIncomplete
	}
	return source, err
}
