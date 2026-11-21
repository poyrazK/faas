package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/api"
)

// buildRegistryMirrors reads the private registry mirrors vmmd serves a
// Dockerfile build (ADR-20261110210000 slice 3j-3). It reads secrets.env
// without removing it: buildSecretEnv reads and removes it later, after
// BuildKit has started with these mirrors in its config.
func buildRegistryMirrors(m api.BuildManifest) ([]api.BuildRegistryMirror, error) {
	if m.Framework != api.FrameworkDockerfile {
		return nil, nil
	}
	values, err := loadSecretsFile(secretsEnvPath, nil)
	if err != nil {
		return nil, fmt.Errorf("read build registry mirrors: %w", err)
	}
	raw, ok := values[api.BuildRegistryMirrorsKey]
	if !ok {
		return nil, nil
	}
	return api.ParseBuildRegistryMirrors(raw)
}

// startBuildRegistryForwarders listens on 127.0.0.1:GuestPort for each
// mirror and relays every connection to the host's vsock port VsockPort,
// where vmmd serves the mirror. BuildKit shares the guest's network
// namespace, so it reaches these listeners. They run until the VM powers
// off.
func startBuildRegistryForwarders(mirrors []api.BuildRegistryMirror) error {
	for _, m := range mirrors {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(m.GuestPort)))
		if err != nil {
			return fmt.Errorf("build registry mirror for %s: %w", m.Registry, err)
		}
		go forwardToVsock(ln, m.VsockPort)
	}
	return nil
}

func forwardToVsock(ln net.Listener, port uint32) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = conn.Close() }()
			host, err := dialHostVsock(port)
			if err != nil {
				return
			}
			defer func() { _ = host.Close() }()
			relay(conn, host)
		}()
	}
}

// relay copies both ways until either side ends, then ends the other.
func relay(a net.Conn, b io.ReadWriteCloser) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(b, a)
		_ = b.Close()
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(a, b)
		_ = a.Close()
	}()
	wg.Wait()
}

// vsockConn is a blocking AF_VSOCK stream to the host; the net package
// cannot wrap AF_VSOCK sockets.
type vsockConn struct {
	fd   int
	once sync.Once
}

func dialHostVsock(port uint32) (*vsockConn, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("vsock socket: %w", err)
	}
	if err := unix.Connect(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_HOST, Port: port}); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("vsock connect %d: %w", port, err)
	}
	return &vsockConn{fd: fd}, nil
}

func (c *vsockConn) Read(p []byte) (int, error) {
	for {
		n, err := unix.Read(c.fd, p)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if n == 0 && len(p) > 0 {
			return 0, io.EOF
		}
		return n, nil
	}
}

func (c *vsockConn) Write(p []byte) (int, error) {
	written := 0
	for written < len(p) {
		n, err := unix.Write(c.fd, p[written:])
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return written, err
		}
		written += n
	}
	return written, nil
}

// Close shuts the stream down first so a Read blocked in the other relay
// goroutine returns, then releases the descriptor once.
func (c *vsockConn) Close() error {
	var err error
	c.once.Do(func() {
		_ = unix.Shutdown(c.fd, unix.SHUT_RDWR)
		err = unix.Close(c.fd)
	})
	return err
}
