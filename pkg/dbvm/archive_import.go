package dbvm

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/pgarchive"
)

func (r *Runtime) ImportArchive(ctx context.Context, id string, request pgarchive.Request, src io.Reader, ready func() error) (dbguest.Response, error) {
	if !request.Valid() {
		return dbguest.Response{}, pgarchive.ErrArchive
	}
	vm, err := r.running(id)
	if err != nil {
		return dbguest.Response{}, err
	}
	conn, err := dialOnce(ctx, r.udsPath(vm), dbguest.ArchiveImportPort)
	if err != nil {
		return dbguest.Response{}, err
	}
	defer conn.Close() //nolint:errcheck
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline := time.Now().Add(pgarchive.UploadTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return dbguest.Response{}, err
	}
	reader := bufio.NewReaderSize(conn, pgarchive.ManifestLimit)
	var reply pgarchive.Reply
	if err := pgarchive.ReadLine(reader, &reply); err != nil {
		return dbguest.Response{}, err
	}
	if !reply.Ready {
		return reply.Result, nil
	}
	if err := ready(); err != nil {
		return dbguest.Response{}, err
	}
	if err := request.Manifest.Copy(conn, src); err != nil {
		return dbguest.Response{}, err
	}
	if _, err := conn.Write([]byte{pgarchive.UploadComplete}); err != nil {
		return dbguest.Response{}, err
	}
	reply = pgarchive.Reply{}
	if err := pgarchive.ReadLine(reader, &reply); err != nil {
		return dbguest.Response{}, err
	}
	if reply.Ready {
		return dbguest.Response{}, pgarchive.ErrArchive
	}
	return reply.Result, nil
}
