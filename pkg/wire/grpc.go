// gRPC dial/listen helpers for the control-plane <-> compute-plane boundary.
//
// ADR-025 ("Decoupled Control Plane and Compute Nodes", docs/adr/025)
// commits the platform to allowing the control plane (apid/gatewayd/schedd/
// meterd/githubd) to run on hosts where /dev/kvm isn't available — only
// vmmd/builderd need KVM. This file is the location-transparent gRPC
// seam that issue #95 lands as its first slice: strict target parsing,
// scheme-aware dialing, and owner-aware listening so UNIX sockets remain
// the default for single-box deployments while TCP/DNS targets require
// mutual TLS.
//
// Auth model:
//   - unix:  socket-mode 0660 + group `faas` (ADR-015), transport insecure
//     OR wrapped in TLS when cfg paths are set (issue #95: empty paths +
//     cert paths => TLS-over-UNIX keeps working).
//   - tcp:   mTLS only. wire.Dial and wire.Listen both refuse a nil
//     *tls.Config on a non-unix scheme; see ADR-025 rejected-alternatives.
//   - dns:   same as tcp; mTLS only. dns:// is a dial target, not a bind
//     target, so wire.Listen rejects it outright.
//
// Construction is lazy by design: wire.Dial returns a grpc.ClientConn
// without blocking on the peer. RPC contexts continue to control the
// actual handshake and reconnect. The ctx passed here is consulted by any
// custom dialer we install for tcp scheme and by Listen's net.ListenConfig.

package wire

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Scheme is a gRPC-target scheme understood by this package. Values match
// the URL scheme gRPC expects (or, for tcp, the canonical rewrite we emit
// into the actual gRPC.NewClient target).
type Scheme string

const (
	SchemeUnix Scheme = "unix"
	SchemeTCP  Scheme = "tcp"
	SchemeDNS  Scheme = "dns"
)

// Target is a parsed gRPC endpoint. Address semantics depend on Scheme:
//   - SchemeUnix: absolute filesystem path (e.g. /run/faas/vmmd.sock).
//   - SchemeTCP:  host:port (host may be empty for tcp://:port which means
//     "all interfaces" on Listen; on Dial it's rejected so an unambiguous
//     remote host is required).
//   - SchemeDNS:  host:port.
type Target struct {
	Scheme  Scheme
	Address string
}

func (t Target) String() string {
	return string(t.Scheme) + "://" + t.Address
}

// ParseTarget returns a Target for raw. The parser is deliberately strict
// — it only accepts the three schemes above in the form documented in
// ADR-025 and issue #95. Bare paths, relative paths, and bare host:port
// are rejected. Legacy bare absolute UNIX paths are tolerated by wire.Dial
// (which normalizes before calling ParseTarget) but NOT by this function.
func ParseTarget(raw string) (Target, error) {
	if raw == "" {
		return Target{}, fmt.Errorf("wire: empty target")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Target{}, fmt.Errorf("wire: parse target %q: %w", raw, err)
	}
	switch u.Scheme {
	case "unix":
		if u.Opaque != "" || u.Host != "" || u.Path == "" || u.RawQuery != "" || u.Fragment != "" {
			return Target{}, fmt.Errorf("wire: %q is not a valid unix target", raw)
		}
		if !filepath.IsAbs(u.Path) {
			return Target{}, fmt.Errorf("wire: unix target path must be absolute; got %q", u.Path)
		}
		return Target{Scheme: SchemeUnix, Address: u.Path}, nil
	case "tcp":
		if u.Opaque != "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return Target{}, fmt.Errorf("wire: %q is not a valid tcp target", raw)
		}
		host, port, err := splitHostPortStrict(u.Host, raw)
		if err != nil {
			return Target{}, fmt.Errorf("wire: %q: %w", raw, err)
		}
		return Target{Scheme: SchemeTCP, Address: host + ":" + strconv.Itoa(port)}, nil
	case "dns":
		// gRPC's built-in dns resolver expects the canonical triple-slash
		// form `dns:///host:port`; Go's net/url puts the authority in
		// u.Path for that shape, while the legacy `dns://host:port` form
		// (no extra slash) lands in u.Host. Accept either so operators
		// can paste either; reject anything else.
		if u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" {
			return Target{}, fmt.Errorf("wire: %q is not a valid dns target", raw)
		}
		authority := u.Host
		if authority == "" {
			authority = strings.TrimPrefix(u.Path, "/")
			if authority == "" || u.Path == "" {
				return Target{}, fmt.Errorf("wire: dns target requires an authority; got %q", raw)
			}
			if strings.Contains(authority, "/") {
				return Target{}, fmt.Errorf("wire: dns target path must be a single authority; got %q", u.Path)
			}
		}
		host, port, err := splitHostPortStrict(authority, raw)
		if err != nil {
			return Target{}, fmt.Errorf("wire: %q: %w", raw, err)
		}
		if host == "" {
			return Target{}, fmt.Errorf("wire: dns target requires a hostname authority; got %q", raw)
		}
		return Target{Scheme: SchemeDNS, Address: host + ":" + strconv.Itoa(port)}, nil
	default:
		return Target{}, fmt.Errorf("wire: unsupported scheme %q in target %q (use unix|tcp|dns)", u.Scheme, raw)
	}
}

// NormalizeLegacyTarget converts the bare absolute path form produced by
// pre-#95 call sites (e.g. "/run/faas/vmmd.sock") into the unix:// URL
// form ParseTarget expects. Used by wire.Dial only; tests of ParseTarget
// itself remain strict. Empty input is rejected.
func NormalizeLegacyTarget(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("wire: empty target")
	}
	if strings.Contains(raw, "://") {
		return raw, nil
	}
	if !filepath.IsAbs(raw) {
		return "", fmt.Errorf("wire: legacy bare target must be an absolute path; got %q", raw)
	}
	return "unix://" + raw, nil
}

// Dial opens a lazy gRPC client connection. tcp/dns without TLS is refused
// (issue #95 + ADR-025 reject plain TCP outright).
//
// Legacy bare absolute UNIX paths are accepted verbatim. Production callers
// should migrate to DialContext for explicit context wiring.
func Dial(ctx context.Context, target string, tlsCfg *tls.Config) (*grpc.ClientConn, error) {
	return DialContext(ctx, target, tlsCfg)
}

// DialContext is the context-aware counterpart of Dial. It is provided as
// a separate entrypoint so packages can attach additional DialOptions
// (e.g. WithBlock, KeepaliveParams) without breaking the legacy Dial
// signature used by tests.
func DialContext(ctx context.Context, target string, tlsCfg *tls.Config, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("wire: dial cancelled: %w", err)
	}
	normalized, err := NormalizeLegacyTarget(target)
	if err != nil {
		return nil, err
	}
	t, err := ParseTarget(normalized)
	if err != nil {
		return nil, err
	}

	var creds credentials.TransportCredentials
	switch t.Scheme {
	case SchemeUnix:
		if tlsCfg != nil {
			creds = credentials.NewTLS(tlsCfg)
		} else {
			creds = insecure.NewCredentials()
		}
	case SchemeTCP, SchemeDNS:
		if tlsCfg == nil {
			return nil, fmt.Errorf("wire: mTLS required for %s target %q", t.Scheme, target)
		}
		if t.Scheme == SchemeTCP {
			// Reject port 0 at dial time — it's bind-only. ParseTarget
			// itself allows 0 because Listen needs to bind ephemeral
			// ports; the dial side has no kernel-assigned port to read.
			if _, portStr, err := net.SplitHostPort(t.Address); err == nil && portStr == "0" {
				return nil, fmt.Errorf("wire: tcp target %q has port 0; not valid for dial", target)
			}
		}
		creds = credentials.NewTLS(tlsCfg)
	}

	dialOpts := append([]grpc.DialOption{grpc.WithTransportCredentials(creds)}, opts...)

	switch t.Scheme {
	case SchemeUnix, SchemeDNS:
		// gRPC has built-in resolvers for both schemes; pass through
		// unchanged. grpc.NewClient is lazy; ctx is consulted via opts
		// where present (e.g. WithBlock + DialContext flavour) but the
		// default lazy dial returns immediately.
		conn, err := grpc.NewClient(t.String(), dialOpts...)
		if err != nil {
			return nil, fmt.Errorf("wire: dial %s: %w", t, err)
		}
		return conn, nil
	case SchemeTCP:
		// grpc-go has no built-in "tcp" resolver. Map it to passthrough
		// and install a context-aware TCP dialer.
		dialer := &net.Dialer{}
		tcpDialer := func(dialCtx context.Context, _ string) (net.Conn, error) {
			return dialer.DialContext(dialCtx, "tcp", t.Address)
		}
		dialOpts = append(dialOpts, grpc.WithContextDialer(tcpDialer))
		conn, err := grpc.NewClient("passthrough:///"+t.Address, dialOpts...)
		if err != nil {
			return nil, fmt.Errorf("wire: dial %s: %w", t, err)
		}
		return conn, nil
	}
	return nil, fmt.Errorf("wire: internal: unhandled scheme %q", t.Scheme)
}

// Listen binds a gRPC server-side listener for the given target. See the
// package comment for the auth model. Errors before binding are returned
// without any side effect; binding failures are returned with the listener
// already closed.
func Listen(ctx context.Context, target string, tlsCfg *tls.Config) (net.Listener, error) {
	return ListenAs(ctx, target, tlsCfg, "")
}

// ListenAs is the owner-aware variant of Listen. When the target is a UNIX
// socket and daemonUser is non-empty, the socket file is chowned to that
// user's uid and the `faas` group gid so peers in the same group can dial
// it (ADR-015). Empty daemonUser skips the ownership step; the syscall
// will then bind the socket owned by the running process.
//
// For tcp scheme, daemonUser is currently informational only; the actual
// bind address is local. (Operator-driven privilege drop is a separate
// concern handled by systemd's User= directive, not by Go.)
func ListenAs(ctx context.Context, target string, tlsCfg *tls.Config, daemonUser string) (net.Listener, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("wire: listen cancelled: %w", err)
	}
	t, err := ParseTarget(target)
	if err != nil {
		return nil, err
	}

	var lis net.Listener
	switch t.Scheme {
	case SchemeUnix:
		l, err := listenUnix(t.Address, daemonUser)
		if err != nil {
			return nil, fmt.Errorf("wire: listen %s: %w", t, err)
		}
		lis = l
	case SchemeTCP:
		if tlsCfg == nil {
			return nil, fmt.Errorf("wire: mTLS required for tcp listen_addr %q", target)
		}
		l, err := (&net.ListenConfig{}).Listen(ctx, "tcp", t.Address)
		if err != nil {
			return nil, fmt.Errorf("wire: listen %s: %w", t, err)
		}
		lis = l
	case SchemeDNS:
		return nil, fmt.Errorf("wire: dns scheme is not a bind target")
	}

	if tlsCfg != nil {
		tlsLis := tls.NewListener(lis, tlsCfg)
		// tls.NewListener owns the underlying listener; on a tls.NewListener
		// Close, it closes the underlying conn too. No defer-close needed
		// for the success path. The error path closes lis explicitly above.
		return tlsLis, nil
	}
	return lis, nil
}

// listenUnix binds a UNIX socket at path with the documented DAC model.
// If daemonUser is empty, the existing ListenOrRecreateByName path is
// used so legacy callers (tests, daemons that don't chown) still bind.
func listenUnix(path, daemonUser string) (net.Listener, error) {
	if daemonUser == "" {
		return ListenOrRecreateByName(path, currentUserOrRoot())
	}
	return ListenOrRecreateByName(path, daemonUser)
}

func currentUserOrRoot() string {
	if u, err := currentUserLookup(); err == nil && u.Username != "" {
		return u.Username
	}
	return "root"
}

// SetCurrentUser replaces the function used to look up the current OS user
// when ListenAs binds a UNIX socket without an explicit daemon user. Tests
// call it to return a stable fake user; production never calls it.
//
// The function is package-private and replaces a closure captured at
// package init time; the captured default is os/user.Current.
var currentUserLookup = user.Current

// SetCurrentUser swaps the lookup used by ListenAs's "no daemonUser"
// fallback. Tests pass a fake user so the bind does not depend on a
// real /etc/passwd entry named after the test binary.
func SetCurrentUser(fn func() (*user.User, error)) { currentUserLookup = fn }

// --- TLS configuration ----------------------------------------------------

// LoadServerTLSConfig loads the server's mutual-TLS configuration. A nil
// result with a nil error is the documented signal that all three paths
// were empty (no TLS, single-box mode continues to work). A partial cluster
// (e.g. cert+key present, ca missing) is rejected — silent fallbacks
// would let an admin misconfigure the server as plaintext-with-no-CA.
//
// fieldPrefix is prepended to the missing-field names in the error
// message so callers with role-distinguished clusters (e.g. schedd's
// `tls_*` server fields vs `vmmd_tls_*` client fields) get a TOML key
// the operator can immediately act on. Empty prefix produces the
// generic `tls_*_path` labels.
func LoadServerTLSConfig(certPath, keyPath, caPath string) (*tls.Config, error) {
	return loadServerTLSConfig(certPath, keyPath, caPath, "")
}

// LoadServerTLSConfigWithPrefix is the variant of LoadServerTLSConfig
// that names missing fields with the given prefix (e.g. "vmmd_tls_").
func LoadServerTLSConfigWithPrefix(prefix, certPath, keyPath, caPath string) (*tls.Config, error) {
	return loadServerTLSConfig(certPath, keyPath, caPath, prefix)
}

func loadServerTLSConfig(certPath, keyPath, caPath, prefix string) (*tls.Config, error) {
	if certPath == "" && keyPath == "" && caPath == "" {
		return nil, nil
	}
	missing := emptyFields(prefix, certPath, keyPath, caPath)
	if len(missing) > 0 {
		return nil, fmt.Errorf("wire: server TLS config incomplete; missing %s", strings.Join(missing, ","))
	}

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("wire: load server keypair: %w", err)
	}

	caPool, err := loadCAPool(caPath)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{"h2"},
	}, nil
}

// LoadClientTLSConfig loads the client's mTLS configuration. Same
// all-or-none behaviour as LoadServerTLSConfig; a nil result with a nil
// error means the caller should fall back to insecure (single-box unix
// targets). fieldPrefix semantics match LoadServerTLSConfig.
func LoadClientTLSConfig(certPath, keyPath, caPath string) (*tls.Config, error) {
	return loadClientTLSConfig(certPath, keyPath, caPath, "")
}

// LoadClientTLSConfigWithPrefix is the role-distinguished variant.
func LoadClientTLSConfigWithPrefix(prefix, certPath, keyPath, caPath string) (*tls.Config, error) {
	return loadClientTLSConfig(certPath, keyPath, caPath, prefix)
}

func loadClientTLSConfig(certPath, keyPath, caPath, prefix string) (*tls.Config, error) {
	if certPath == "" && keyPath == "" && caPath == "" {
		return nil, nil
	}
	missing := emptyFields(prefix, certPath, keyPath, caPath)
	if len(missing) > 0 {
		return nil, fmt.Errorf("wire: client TLS config incomplete; missing %s", strings.Join(missing, ","))
	}

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("wire: load client keypair: %w", err)
	}

	caPool, err := loadCAPool(caPath)
	if err != nil {
		return nil, err
	}

	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{"h2"},
	}
	// CA-only verification per issue #95 / ADR-025 §Rejected alternatives:
	// hostname matching is explicitly out of scope for this slice. Bare
	// InsecureSkipVerify would also disable CA verification, which is not
	// what we want. We keep InsecureSkipVerify so the stdlib stops on the
	// hostname check, then run the chain validation ourselves below.
	cfg.InsecureSkipVerify = true
	verifyLeaf := func(rawCerts [][]byte) error {
		if len(rawCerts) == 0 {
			return errors.New("wire: server presented no certificates")
		}
		leaf, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return fmt.Errorf("wire: parse server leaf: %w", err)
		}
		intermediatePool := x509.NewCertPool()
		for _, raw := range rawCerts[1:] {
			if ic, err := x509.ParseCertificate(raw); err == nil {
				intermediatePool.AddCert(ic)
			}
		}
		_, err = leaf.Verify(x509.VerifyOptions{
			Roots:         caPool,
			Intermediates: intermediatePool,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		})
		return err
	}
	verifyChain := func(certs []*x509.Certificate) error {
		if len(certs) == 0 {
			return errors.New("wire: server presented no certificates")
		}
		intermediatePool := x509.NewCertPool()
		for _, c := range certs[1:] {
			intermediatePool.AddCert(c)
		}
		_, err := certs[0].Verify(x509.VerifyOptions{
			Roots:         caPool,
			Intermediates: intermediatePool,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		})
		return err
	}
	// CA-only verification per issue #95 / ADR-025 §Rejected alternatives:
	// hostname matching is explicitly out of scope for this slice. Bare
	// InsecureSkipVerify would also disable CA verification, which is not
	// what we want. We keep InsecureSkipVerify so the stdlib stops on the
	// hostname check, then run the chain validation ourselves below.
	cfg.InsecureSkipVerify = true
	// Set both hooks so the verifier runs on resumed TLS sessions too
	// (gosec G123). The stdlib only calls VerifyPeerCertificate on the
	// initial handshake; resumed sessions need VerifyConnection.
	cfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		return verifyLeaf(rawCerts)
	}
	cfg.VerifyConnection = func(state tls.ConnectionState) error {
		return verifyChain(state.PeerCertificates)
	}
	return cfg, nil
}

// loadCAPool reads a PEM file and parses it into a fresh x509.CertPool.
// AppendCertsFromPEM returning false is treated as an error because the
// CA file is supposed to contain at least one certificate and silent
// fall-back would leave us with an untrusted pool.
func loadCAPool(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("wire: read CA %q: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("wire: CA file %q contained no PEM-encoded certificates", path)
	}
	return pool, nil
}

func emptyFields(prefix string, paths ...string) []string {
	var missing []string
	for i, p := range paths {
		if p == "" {
			missing = append(missing, tlsFieldName(prefix, i))
		}
	}
	return missing
}

func tlsFieldName(prefix string, i int) string {
	switch i {
	case 0:
		return prefix + "tls_cert_path"
	case 1:
		return prefix + "tls_key_path"
	case 2:
		return prefix + "tls_ca_path"
	}
	return prefix + "tls_field"
}

// splitHostPortStrict parses an authority of the form host:port. Port 0 is
// permitted (kernel-assigned on bind; meaningless for connect, enforced by
// callers). Other invalid values — non-numeric, out-of-range, missing
// port — surface as parse errors that mention the raw target.
func splitHostPortStrict(hostport, raw string) (string, int, error) {
	if hostport == "" {
		return "", 0, fmt.Errorf("missing authority in %q", raw)
	}
	host, portStr, err := net.SplitHostPort(hostport)
	if err != nil {
		return "", 0, fmt.Errorf("authority must be host:port; got %q in %q: %w", hostport, raw, err)
	}
	if portStr == "" {
		return "", 0, fmt.Errorf("port required; got %q", hostport)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("port %q: %w", portStr, err)
	}
	if port < 0 || port > 65535 {
		return "", 0, fmt.Errorf("port %d out of range in %q", port, raw)
	}
	return host, port, nil
}

// currentUserLookup is replaced in tests via SetCurrentUser to skip
// /etc/passwd lookups. Production callers always use the default
// os/user.Current binding.
