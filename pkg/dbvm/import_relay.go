package dbvm

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// Importing another PostgreSQL database (ADR-1027 migration off Neon). The
// guest's import tools connect to the host's import port; the runtime
// relays each connection to the one source the control plane named, and
// secures it: it asks the source for TLS the way libpq does (SSLRequest),
// verifies the source's certificate for its host name, and refuses
// addresses that are not public, so an import can reach nothing on the
// platform's networks. The guest only ever speaks plaintext to the relay.

// ImportSource is a source PostgreSQL server's network endpoint.
type ImportSource struct {
	Host string
	Port int
}

var importHostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// ErrInvalidImportSource: not a host name or address and a port.
var ErrInvalidImportSource = errors.New("dbvm: invalid import source")

// Validate rejects an endpoint that is not a host name or address and port.
func (s ImportSource) Validate() error {
	if _, err := netip.ParseAddr(s.Host); err != nil && !importHostPattern.MatchString(s.Host) {
		return fmt.Errorf("%w: host", ErrInvalidImportSource)
	}
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("%w: port", ErrInvalidImportSource)
	}
	return nil
}

const (
	// importDialTimeout bounds reaching the source and its TLS handshake.
	importDialTimeout = 30 * time.Second
	// importStreamsPerVM caps an import's concurrent source connections.
	importStreamsPerVM = 4
)

// sslRequest is PostgreSQL's request to switch a connection to TLS.
var sslRequest = []byte{0, 0, 0, 8, 0x04, 0xd2, 0x16, 0x2f}

// importUDSPath is where Firecracker delivers the guest's connections to
// the import port.
func (r *Runtime) importUDSPath(vm *databaseVM) string {
	return r.udsPath(vm) + "_" + strconv.FormatUint(uint64(dbguest.ImportPort), 10)
}

// SetImportSource lets a running database's import reach src. The relay
// listens from the first call until the VM stops; a later call changes
// where new connections go.
func (r *Runtime) SetImportSource(id string, src ImportSource) error {
	if err := src.Validate(); err != nil {
		return err
	}
	vm, err := r.running(id)
	if err != nil {
		return err
	}
	vm.importSrc.Store(&src)
	r.mu.Lock()
	defer r.mu.Unlock()
	if vm.importLn != nil {
		return nil
	}
	path := r.importUDSPath(vm)
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("dbvm: import relay: %w", err)
	}
	// Firecracker runs as the jail's uid and connects to this socket.
	if err := os.Chown(path, vm.lease.UID, vm.lease.GID); err != nil {
		_ = ln.Close()
		return fmt.Errorf("dbvm: import relay: %w", err)
	}
	vm.importLn = ln
	slots := make(chan struct{}, importStreamsPerVM)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			select {
			case slots <- struct{}{}:
			default:
				_ = conn.Close()
				continue
			}
			go func() {
				defer func() { <-slots }()
				r.relayImport(conn, *vm.importSrc.Load())
			}()
		}
	}()
	return nil
}

func (r *Runtime) relayImport(guest net.Conn, src ImportSource) {
	defer func() { _ = guest.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), importDialTimeout)
	remote, err := r.dialImportSource(ctx, src)
	cancel()
	if err != nil {
		return
	}
	defer func() { _ = remote.Close() }()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, guest); done <- struct{}{} }()
	go func() { _, _ = io.Copy(guest, remote); done <- struct{}{} }()
	<-done
}

// ErrImportSourceRefused: the source is not reachable the way an import
// requires: a public address, TLS, a certificate for its host name.
var ErrImportSourceRefused = errors.New("dbvm: import source refused")

// dialImportSource connects to the source over verified TLS. The host name
// is resolved once and its address checked, so a later answer cannot point
// the connection elsewhere.
func (r *Runtime) dialImportSource(ctx context.Context, src ImportSource) (net.Conn, error) {
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", src.Host)
	if err != nil {
		return nil, fmt.Errorf("dbvm: resolve import source: %w", err)
	}
	var conn net.Conn
	var errs []error
	for _, a := range addrs {
		if !r.cfg.ImportAllowPrivate && !publicAddr(a) {
			continue
		}
		var d net.Dialer
		c, err := d.DialContext(ctx, "tcp", netip.AddrPortFrom(a.Unmap(), uint16(src.Port)).String()) //nolint:gosec // port validated
		if err == nil {
			conn = c
			break
		}
		errs = append(errs, err)
	}
	if conn == nil && len(errs) == 0 {
		return nil, fmt.Errorf("%w: %s has no public address", ErrImportSourceRefused, src.Host)
	}
	if conn == nil {
		return nil, fmt.Errorf("dbvm: dial import source: %w", errors.Join(errs...))
	}
	tlsConn, err := startPostgresTLS(ctx, conn, src.Host, r.cfg.ImportRoots)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return tlsConn, nil
}

// startPostgresTLS asks a PostgreSQL server for TLS and completes the
// handshake, verifying its certificate for host against roots (nil: the
// system's).
func startPostgresTLS(ctx context.Context, conn net.Conn, host string, roots *x509.CertPool) (net.Conn, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
		defer func() { _ = conn.SetDeadline(time.Time{}) }()
	}
	if _, err := conn.Write(sslRequest); err != nil {
		return nil, fmt.Errorf("dbvm: import source TLS request: %w", err)
	}
	var answer [1]byte
	if _, err := io.ReadFull(conn, answer[:]); err != nil {
		return nil, fmt.Errorf("dbvm: import source TLS answer: %w", err)
	}
	if answer[0] != 'S' {
		return nil, fmt.Errorf("%w: the source does not offer TLS", ErrImportSourceRefused)
	}
	tlsConn := tls.Client(conn, &tls.Config{ServerName: host, RootCAs: roots, MinVersion: tls.VersionTLS12})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrImportSourceRefused, err)
	}
	return tlsConn, nil
}

// nonPublic are ranges an import never reaches besides the standard
// private, loopback, link-local and multicast ones: shared address space
// (the overlay), private TCP service addresses and other special-purpose
// blocks.
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("2001:db8::/32"),
}

// publicAddr reports whether an import may connect to a.
func publicAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(a) {
			return false
		}
	}
	return true
}
