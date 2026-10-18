//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	repairguest "github.com/onebox-faas/faas/pkg/datarepair/guest"
	"golang.org/x/sys/unix"
)

const dataRepairManifestRelativePath = core.RehearsalManifestPath

func validateDataRepairManifest(b []byte) error {
	var m struct {
		Kind    string `json:"kind"`
		Version int    `json:"version"`
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil || d.Decode(new(any)) != io.EOF || m.Kind != "data_repair" || m.Version != 1 {
		return errors.New("invalid data repair marker")
	}
	return nil
}

func serveDataRepairOnce(ctx context.Context, ln net.Listener, run func(context.Context, core.RehearsalInput) repairguest.Result) error {
	stop := context.AfterFunc(ctx, func() { _ = ln.Close() })
	defer stop()
	conn, err := ln.Accept()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	stopConn := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopConn()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	in, err := core.ReadRehearsalBody(conn)
	if err != nil {
		return err
	}
	result := run(ctx, in)
	if err = json.NewEncoder(conn).Encode(result); err != nil {
		return err
	}
	return awaitAppTaskHostClose(ctx, conn)
}

func runDataRepairGuest(log *slog.Logger) error {
	defer func() { _ = poweroffAppTask() }()
	interfaces, err := net.Interfaces()
	if err != nil {
		return errors.New("repair guest: cannot verify network isolation")
	}
	for _, iface := range interfaces {
		if iface.Name != "lo" {
			return errors.New("repair guest: network interface present")
		}
	}
	const workspace = "/repair"
	if err = os.MkdirAll(workspace, 0o700); err != nil {
		return errors.New("repair guest: workspace unavailable")
	}
	if err = unix.Mount("tmpfs", workspace, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "size="+strconv.Itoa(api.DataRepairSandboxDiskBytes)); err != nil {
		return errors.New("repair guest: workspace limit unavailable")
	}
	defer func() { _ = unix.Unmount(workspace, 0) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	ln, err := listenGuestVsock(core.RehearsalVsockPort)
	if err != nil {
		return fmt.Errorf("repair guest: listener unavailable")
	}
	defer func() { _ = ln.Close() }()
	err = serveDataRepairOnce(ctx, ln, func(ctx context.Context, in core.RehearsalInput) repairguest.Result {
		return repairguest.Run(ctx, in, workspace)
	})
	if err != nil {
		log.Warn("repair rehearsal guest stopped", "error_class", "unavailable")
	}
	return err
}
