package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/hosting/localpreview"
)

func cmdSitesServe(args []string) int {
	enabled := os.Getenv("FAAS_FRONTEND_HOSTING_LOCAL_PREVIEW_ENABLED")
	if enabled != "1" && enabled != "true" {
		return printErr("Local hosting preview is disabled", errors.New("set FAAS_FRONTEND_HOSTING_LOCAL_PREVIEW_ENABLED=1; no API credentials are needed"))
	}
	f := newFlagSet("sites-serve", flag.ContinueOnError)
	dir := f.String("path", "dist", "prebuilt output directory to snapshot")
	port := f.Int("port", api.HostingLocalPreviewPort, "IPv4 loopback port; 0 selects an available port")
	spa := f.Bool("spa", false, "SPA navigation fallback, matching deployment settings")
	if err := f.Parse(args); err != nil || rejectUnexpectedFlagArgs(f) {
		return 1
	}
	if *port < 0 || *port > 65535 {
		return printErr("Invalid local preview port", errors.New("port must be between 0 and 65535"))
	}
	snapshot, err := hostingLocalSnapshot(*dir, *spa)
	if err != nil {
		return printErr("Invalid prebuilt output; no preview listener was started", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = serveHostingLocal(ctx, snapshot, *port, func(address string) error {
		_, err := fmt.Fprintf(osStdout, "Local frontend preview: http://%s\nSnapshot validated. Restart after rebuilding. Access, TLS and CDN behavior require separate qualification.\n", address)
		return err
	})
	if err != nil {
		return printErr("Local preview stopped", err)
	}
	return 0
}
func hostingLocalSnapshot(dir string, spa bool) (*localpreview.Snapshot, error) {
	// Bind reads to the selected root before hashing through the existing deploy
	// validator. A renamed root or replaced file must still match the manifest.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	manifest, err := staticHostingManifest(dir, spa)
	if err != nil {
		return nil, err
	}

	manifest.Config, err = hostingLocalConfig(root)
	if err != nil {
		return nil, err
	}
	return localpreview.New(manifest, func(file api.HostingFile) ([]byte, error) {
		return hostingLocalRead(root, filepath.FromSlash(file.Path), file.Size)
	})
}
func hostingLocalConfig(root *os.Root) (*api.HostingReleaseConfig, error) {
	if _, err := root.Lstat(hosting.ReleaseConfigFilename); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	enabled := os.Getenv("FAAS_FRONTEND_HOSTING_CONFIG_ENABLED")
	if enabled != "1" && enabled != "true" {
		return nil, errors.New("release configuration requires FAAS_FRONTEND_HOSTING_CONFIG_ENABLED=1")
	}
	data, err := hostingLocalRead(root, hosting.ReleaseConfigFilename, api.HostingConfigMaxBytes)
	if err != nil {
		return nil, err
	}
	return hosting.ParseReleaseConfig(data)
}
func hostingLocalRead(root *os.Root, name string, maxBytes int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("build contains an unavailable or non-regular file")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > maxBytes {
		return nil, errors.New("build file changed before snapshot")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("build file exceeds snapshot bound")
	}
	return data, nil
}
func serveHostingLocal(ctx context.Context, snapshot *localpreview.Snapshot, port int, ready func(string) error) error {
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	handler, err := snapshot.Handler(listener.Addr().String())
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: api.HostingLocalPreviewHeaderTimeout, ReadTimeout: api.HostingLocalPreviewReadTimeout, WriteTimeout: api.HostingLocalPreviewWriteTimeout, IdleTimeout: api.HostingLocalPreviewIdleTimeout, MaxHeaderBytes: api.HostingLocalPreviewHeaderMaxBytes}
	defer func() { _ = server.Close() }()
	if err = ready(listener.Addr().String()); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.HostingLocalPreviewShutdownTimeout)
		defer cancel()
		err = server.Shutdown(shutdown)
		serveErr := <-done
		if err != nil {
			return err
		}
		if !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
		return nil
	}
}
