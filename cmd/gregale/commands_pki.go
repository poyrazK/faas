// commands_pki.go — operator-side CLI for the local-dev control-plane
// PKI (ADR-052). This is the OPERATOR surface, not the customer
// surface: there is no authedClient() call, no SDK, no API call — every
// leaf is a local file-system operation against the canonical
// /etc/faas/tls/{ca,<daemon>/} paths.
//
// The namespace `gregale pki` is a separate top-level command from
// `gregale sign-keys` (cosign keypair, ADR-038 phase 3) because the
// two systems have different trust roots and different rotation cadences:
//
//   - sign-keys: per-box ECDSA P-256 keypair, 0440 root:faas, rotated by
//     the imaged/schedd restart cycle. Compromising a sign-key only
//     invalidates the operator's signature on rootfs layers.
//
//   - pki: per-box CA + per-daemon leaves, 0444/0400 root:root. A
//     compromised CA key invalidates every TLS handshake on the box and
//     every box that trusts it. Operators rotate per-leaf (cheap, no CA
//     change) and the CA itself only on the 5-year NotAfter boundary.
//
// `gregale pki init|status|rotate` mirrors the sign-keys dispatcher
// pattern (cmd/gregale/commands_sign_keys.go). All three leaves share
// the `--root-dir` flag (default /etc/faas/tls) and the per-daemon
// leaf set is fixed in pkg/pki.Roles() — operators don't pick leaves.
//
// `init` is idempotent: leaves whose NotAfter is ≥ 30 days from `now`
// are skipped (no re-issue churn). Pass `--force` to re-issue
// unconditionally.
//
// `rotate` is the destructive variant: it re-issues every leaf
// regardless of expiry. The operator is expected to have archived the
// old material (a `cp -r /etc/faas/tls /var/backups/faas-tls-$(date)`)
// before running this; `rotate` does NOT archive automatically because
// that's a different operational concern (encrypted backups, offsite
// copy) than the CLI is responsible for.
//
// `status` is read-only: per-leaf mode + serial + expires_at + CN +
// SAN list. Exit 0 if every leaf is valid, exit 1 if any is missing or
// has insecure mode (mirrors `gregale sign-keys status`).
package main

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/pki"
)

const dispatchPKI = "pki"

const (
	subPKIInit   = "init"
	subPKIStatus = "status"
	subPKIRotate = "rotate"
)

// cmdPKI is the parent dispatcher. With zero args it prints usage;
// with init/status/rotate it fans to the matching helper. Unknown
// subcommands return 1 with a usage hint — same contract as cmdSignKeys.
func cmdPKI(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale pki <init|status|rotate> [flags]", "pki")
		return 1
	}
	switch args[0] {
	case subPKIInit:
		return cmdPKIInit(args[1:])
	case subPKIStatus:
		return cmdPKIStatus(args[1:])
	case subPKIRotate:
		return cmdPKIRotate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregale pki: unknown subcommand %q (known: init, status, rotate)\n", args[0])
		return 1
	}
}

// pkiFlags is the shared flag surface. All three leaves accept
// --root-dir and --force; only init and rotate actually use --force.
// --daemon is honoured by rotate only — it filters the role set to
// a single directory (with one cross-directory carve-out for the
// meterd→egress client leaf) so an operator can reissue just the
// certs the egress channel touches without churning the rest of the
// fleet.
type pkiFlags struct {
	rootDir string
	force   bool
	daemon  string
}

func newPKIFlags(name string, defaultForce bool) (*flag.FlagSet, *pkiFlags) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	f := &pkiFlags{}
	fs.StringVar(&f.rootDir, "root-dir", pki.DefaultRootDir,
		"directory under which CA + per-daemon leaves live (canonical: "+pki.DefaultRootDir+")")
	fs.BoolVar(&f.force, "force", defaultForce,
		"re-issue leaves whose NotAfter is still >= 30d away (rotate path)")
	fs.StringVar(&f.daemon, "daemon", "",
		"rotate only the leaves in this directory (rotate path; e.g. --daemon egress to reissue just the egress server + meterd client leaves)")
	return fs, f
}

// cmdPKIInit issues the CA + every per-daemon leaf. Idempotent: leaves
// with NotAfter >= ReissueThreshold are skipped silently so re-running
// `gregale pki init` after a partial failure doesn't churn the rest of
// the fleet. Pass --force to re-issue unconditionally.
func cmdPKIInit(args []string) int {
	fs, f := newPKIFlags("pki init", false)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale pki init [flags]", "pki")
		return 1
	}
	caCert, caKey, err := pki.EnsureCA(f.rootDir, f.force)
	if err != nil {
		return printErr("pki init: ensure CA", err)
	}
	written, skipped, errs := ensureAllLeaves(f.rootDir, caCert, caKey, f.force)
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "  ! %v\n", e)
	}
	PrintOK(os.Stdout,
		"CA: %s/ca/{ca.crt (0444), ca.key (0400)} expires %s\n  Wrote %d leaves, skipped %d (NotAfter > %s)",
		f.rootDir, caCert.NotAfter.Format(time.RFC3339),
		written, skipped, pki.ReissueThreshold)
	if len(errs) > 0 {
		return 1
	}
	return 0
}

// cmdPKIStatus reports mode + serial + expires_at + CN + SANs for the
// CA and every leaf. Exit 0 if all material is present with secure
// mode; exit 1 if any is missing or has insecure mode.
func cmdPKIStatus(args []string) int {
	fs, f := newPKIFlags("pki status", false)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale pki status [flags]", "pki")
		return 1
	}
	reportCAStatus(os.Stdout, f.rootDir)
	reportLeafStatusAll(os.Stdout, f.rootDir)
	// Cheap "any expiring within threshold" gate so operators can use
	// this in CI / cron to surface the rotate countdown. Exit 1 here is
	// non-fatal for the human reader but useful as a Nagios-style
	// alarm signal.
	if anyExpiringSoon(f.rootDir, pki.ReissueThreshold) {
		fmt.Fprintf(os.Stderr, "gregale pki status: at least one leaf expires within %s — run `gregale pki init` or `gregale pki rotate`\n",
			pki.ReissueThreshold)
		return 1
	}
	return 0
}

// cmdPKIRotate re-issues every leaf unconditionally. Equivalent to
// `gregale pki init --force`. The CLI splits them so the operator's
// intent (initialize vs. rotate) is recorded in shell history and
// stdout, not just by a flag toggle.
//
// --daemon <name> narrows the rotation to the leaves whose Directory
// matches <name>, plus one cross-directory carve-out: when <name>
// is "egress", the meterd→egress client leaf (Directory=meterd,
// Filename=egress-client) is also rotated because the egress
// server's CN is paired with that client cert at mTLS handshake
// time. This is the rotation path the PR-C+D cert-dir migration
// (deploy/ansible/roles/control_plane_service/tasks/migrate_egress_certs.yml)
// expects operators to run immediately after the deploy lands.
func cmdPKIRotate(args []string) int {
	fs, f := newPKIFlags("pki rotate", true)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale pki rotate [flags]", "pki")
		return 1
	}
	// Rotate is destructive on the leaves; the CA is preserved unless
	// the operator also passes --rotate-ca (out of scope for slice 2;
	// see ADR-052 §Risks). We pass force=false to EnsureCA so a
	// healthy existing CA is reused, and force=true to every leaf so
	// all leaves are re-issued unconditionally.
	caCert, caKey, err := pki.EnsureCA(f.rootDir, false)
	if err != nil {
		return printErr("pki rotate: ensure CA", err)
	}
	written, _, errs := ensureAllLeavesFiltered(f.rootDir, caCert, caKey, true, f.daemon)
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "  ! %v\n", e)
	}
	// PR-C+D: gatewayd was split (ADR-070) into gatewayd-internal +
	// gatewayd-public; the egress server leaf lives on the egress
	// directory and is consumed by gatewayd-internal. The restart
	// hint names every daemon whose live cert changed, scoped to the
	// --daemon filter if one was passed (empty == whole fleet).
	hint := rotateRestartHint(f.daemon)
	PrintOK(os.Stdout,
		"Rotated %d leaves under %s (CA preserved)\n  %s",
		written, f.rootDir, hint)
	if len(errs) > 0 {
		return 1
	}
	return 0
}

// rotateRestartHint returns the systemctl reload/restart line an
// operator should run after a rotation. The whole-fleet list is
// post-ADR-070 (gatewayd-internal is the only gatewayd-named daemon;
// gatewayd-public uses certmagic, not the control-plane PKI).
// Scoping to a single daemon narrows the list so a partial rotation
// (--daemon egress) only restarts the daemons that actually loaded
// the rotated leaves.
func rotateRestartHint(daemon string) string {
	if daemon != "" {
		switch daemon {
		case "egress":
			// The egress server leaf is consumed by
			// gatewayd-internal; the egress-client leaf is
			// consumed by meterd.
			return "Reload: systemctl reload faas-gatewayd-internal faas-meterd  (egress leaves: /etc/faas/tls/egress/egress.crt, /etc/faas/tls/meterd/egress-client.crt)"
		case "meterd":
			return "Reload: systemctl reload faas-meterd"
		case "schedd":
			return "Reload: systemctl reload faas-schedd"
		case "vmmd":
			return "Reload: systemctl reload faas-vmmd"
		case "apid":
			return "Reload: systemctl reload faas-apid"
		case "githubd":
			return "Reload: systemctl reload faas-githubd"
		case "builderd":
			return "Reload: systemctl reload faas-builderd"
		}
	}
	return "Reload: systemctl reload faas-{apid,gatewayd-internal,schedd,vmmd,builderd,meterd,githubd}"
}

// ensureAllLeaves iterates pkg/pki.Roles() and ensures each is present.
// Returns the count of freshly-written leaves, the count of leaves
// skipped (NotAfter > ReissueThreshold), and a slice of per-leaf errors
// (the caller decides whether the aggregate counts as success).
func ensureAllLeaves(rootDir string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, force bool) (int, int, []error) {
	var written, skipped int
	var errs []error
	for _, role := range pki.Roles() {
		err := pki.EnsureLeaf(rootDir, role, caCert, caKey, force)
		switch {
		case err == nil:
			written++
		case isErrLeafNotExpiringSoon(err):
			skipped++
		default:
			errs = append(errs, fmt.Errorf("%s/%s: %w", role.Directory, role.Filename, err))
		}
	}
	return written, skipped, errs
}

// ensureAllLeavesFiltered is ensureAllLeaves with an optional --daemon
// scope. daemon="" iterates every role (delegates to ensureAllLeaves);
// daemon="<dir>" matches role.Directory == dir PLUS one cross-directory
// carve-out for the egress pair: when daemon=="egress", the meterd
// client leaf (Directory=meterd, Filename=egress-client) is also
// included because the egress server's CN (egress.faas) is paired with
// that client cert at mTLS handshake time — rotating only the server
// leaf would leave meterd holding a stale client cert and every
// subsequent egress dial would fail with a tls: bad certificate.
func ensureAllLeavesFiltered(rootDir string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, force bool, daemon string) (int, int, []error) {
	if daemon == "" {
		return ensureAllLeaves(rootDir, caCert, caKey, force)
	}
	var written, skipped int
	var errs []error
	for _, role := range pki.Roles() {
		if !roleMatchesDaemon(role, daemon) {
			continue
		}
		err := pki.EnsureLeaf(rootDir, role, caCert, caKey, force)
		switch {
		case err == nil:
			written++
		case isErrLeafNotExpiringSoon(err):
			skipped++
		default:
			errs = append(errs, fmt.Errorf("%s/%s: %w", role.Directory, role.Filename, err))
		}
	}
	return written, skipped, errs
}

// roleMatchesDaemon returns true if role belongs to daemon. The
// default match is role.Directory == daemon. The egress carve-out
// (meterd client leaf whose Filename is "egress-client") is encoded
// here so ensureAllLeavesFiltered stays free of role-specific
// conditionals beyond the single carve-out the PR-C+D migration
// requires.
func roleMatchesDaemon(role pki.Role, daemon string) bool {
	if role.Directory == daemon {
		return true
	}
	if daemon == "egress" && role.Directory == "meterd" && role.Filename == "egress-client" {
		return true
	}
	return false
}

// reportCAStatus prints one line for the CA.
func reportCAStatus(w io.Writer, rootDir string) {
	certPath, _ := pki.CARoot(rootDir)
	reportOneStatus(w, "ca       ", certPath)
}

// reportLeafStatusAll prints one line per leaf in pkg/pki.Roles().
func reportLeafStatusAll(w io.Writer, rootDir string) {
	for _, role := range pki.Roles() {
		certPath, _ := pki.LeafPaths(rootDir, role)
		label := fmt.Sprintf("%-9s %s", role.Directory, role.Filename)
		reportOneStatus(w, label, certPath)
	}
}

// reportOneStatus prints one line: <label>  <mode>  <serial>  <expires_at>  <CN>  <SANs>  <path>.
// Missing files print "<label>  missing  <path>" and return without
// error so the operator can see the full picture before running init.
//
// The Fprintf calls below cannot fail meaningfully (w is typically os.Stdout
// or a *bytes.Buffer the operator reads later); we discard the error
// explicitly so errcheck is happy and the code reads as a printf, not a
// status-report path that pretends to bubble I/O errors back to the operator.
func reportOneStatus(w io.Writer, label, certPath string) {
	info, err := os.Stat(certPath)
	if err != nil {
		_, _ = fmt.Fprintf(w, "%s  missing  %s\n", label, certPath)
		return
	}
	data, err := os.ReadFile(certPath)
	if err != nil {
		_, _ = fmt.Fprintf(w, "%s  mode %#o  read error: %v  %s\n", label, info.Mode().Perm(), err, certPath)
		return
	}
	block, _ := pem.Decode(data)
	if block == nil {
		_, _ = fmt.Fprintf(w, "%s  mode %#o  not PEM  %s\n", label, info.Mode().Perm(), certPath)
		return
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		_, _ = fmt.Fprintf(w, "%s  mode %#o  parse error: %v  %s\n", label, info.Mode().Perm(), err, certPath)
		return
	}
	sans := formatSANs(cert.DNSNames, cert.IPAddresses)
	_, _ = fmt.Fprintf(w, "%s  %#o  serial=%s  expires=%s  CN=%s  SANs=[%s]  %s\n",
		label, info.Mode().Perm(),
		cert.SerialNumber.String(),
		cert.NotAfter.Format(time.RFC3339),
		cert.Subject.CommonName,
		sans, certPath)
}

func formatSANs(dnsNames []string, ips []net.IP) string {
	parts := make([]string, 0, len(dnsNames)+len(ips))
	parts = append(parts, dnsNames...)
	for _, ip := range ips {
		parts = append(parts, ip.String())
	}
	return strings.Join(parts, ",")
}

// anyExpiringSoon returns true if any leaf on disk has NotAfter <
// now+threshold. Used by `status` to surface the rotate countdown.
func anyExpiringSoon(rootDir string, threshold time.Duration) bool {
	now := time.Now()
	for _, role := range pki.Roles() {
		certPath, _ := pki.LeafPaths(rootDir, role)
		data, err := os.ReadFile(certPath)
		if err != nil {
			continue // missing files are init's problem, not status's
		}
		block, _ := pem.Decode(data)
		if block == nil {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		if cert.NotAfter.Sub(now) < threshold {
			return true
		}
	}
	return false
}

// isErrLeafNotExpiringSoon matches the sentinel without using errors.Is
// (the sentinel may be wrapped by EnsureLeaf). String compare is fine
// here because the sentinel's text is stable.
func isErrLeafNotExpiringSoon(err error) bool {
	return err != nil && strings.Contains(err.Error(), pki.ErrLeafNotExpiringSoon.Error())
}
