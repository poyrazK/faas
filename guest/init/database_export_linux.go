//go:build linux

package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/pgexport"
)

func (g *databaseGuest) serveExports(listener net.Listener) {
	// One active dump per database. The export listener stays separate from
	// control so status, shutdown and WAL archiving remain responsive.
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		g.exportDatabase(conn)
	}
}

func (g *databaseGuest) exportDatabase(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	if g.currentState() != dbguest.StateReady {
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(databaseControlTimeout))
	var database string
	if err := json.NewDecoder(io.LimitReader(conn, 256)).Decode(&database); err != nil || !pgexport.ValidDatabase(database) {
		return
	}
	deadline := time.Now().Add(pgexport.Timeout)
	_ = conn.SetWriteDeadline(deadline)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	stream := pgexport.StreamWriter{Writer: conn}
	source, err := pgexport.Capture(ctx, databaseBinDir(g.major), databaseSocketDir, database, g.asSuperuser, stream)
	if err == nil && source.Major == g.major {
		_ = stream.Finish(source)
	}
}
