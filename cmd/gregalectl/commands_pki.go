// commands_pki.go — operator-side CLI for the local-dev control-plane
// PKI (ADR-052). This is the OPERATOR surface, not the customer
// surface: there is no authedClient() call, no SDK, no API call — every
// leaf is a local file-system operation against the canonical
// /etc/faas/tls/{ca,<daemon>/} paths.
//
// The namespace `gregalectl pki` is a separate top-level command from
// `gregalectl sign-keys` (cosign keypair, ADR-038 phase 3) because the
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
// `gregalectl pki init|status|rotate` mirrors the sign-keys dispatcher
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
// has insecure mode (mirrors `gregalectl sign-keys status`).
package main

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/pki"
)

const dispatchPKI = "pki"

const (
	subPKIInit           = "init"
	subPKIStatus         = "status"
	subPKIRotate         = "rotate"
	subPKIList           = "list"
	subPKIIssueBundle    = "issue-bundle"
	subPKIInstallBundle  = "install-bundle"
	subPKIExportBundle   = "export-bundle"
	subPKIFingerprint    = "fingerprint"
	subPKIRecoverInstall = "recover-install"
	subPKIMetrics        = "metrics"
)

// cmdPKI is the parent dispatcher. With zero args it prints usage;
// with init/status/rotate it fans to the matching helper. Unknown
// subcommands return 1 with a usage hint — same contract as cmdSignKeys.
func cmdPKI(args []string) int {
	parent, _ := lookupCliCommand("pki")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregalectl pki <init|status|list|rotate|export-bundle|issue-bundle|install-bundle|recover-install|fingerprint|metrics> [flags]", "pki")
		return 1
	}
	switch args[0] {
	case subPKIInit:
		return cmdPKIInit(args[1:])
	case subPKIStatus:
		return cmdPKIStatus(args[1:])
	case subPKIList:
		return cmdPKIList(args[1:])
	case subPKIRotate:
		return cmdPKIRotate(args[1:])
	case subPKIIssueBundle:
		return cmdPKIIssueBundle(args[1:])
	case subPKIInstallBundle:
		return cmdPKIInstallBundle(args[1:])
	case subPKIRecoverInstall:
		return cmdPKIRecoverInstall(args[1:])
	case subPKIExportBundle:
		return cmdPKIExportBundle(args[1:])
	case subPKIFingerprint:
		return cmdPKIFingerprint(args[1:])
	case subPKIMetrics:
		return cmdPKIMetrics(args[1:])
	default:
		sug, _ := suggestSubcommand(args[0], parent)
		fmt.Fprintf(os.Stderr, "gregalectl pki: unknown subcommand %q (known: init, status, list, rotate, export-bundle, issue-bundle, install-bundle, recover-install, fingerprint, metrics)\n", args[0])
		maybeSuggestSub(sug)
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
	// nodeCN and transportSAN are used when a compute-only box is
	// provisioned from operator-owned PKI material. Leaves that authenticate
	// the host to a control-plane verifier use nodeCN while every selected
	// leaf gets transportSAN for endpoint validation.
	nodeCN       string
	transportSAN string
	// boxRole selects the per-box PKI subset (Gate-B PR-3). Empty
	// preserves the pre-Gate-B posture of issuing every leaf from
	// pkg/pki.Roles() — that's the canonical single-box dev/lima
	// shape, and the implicit default for any operator who hasn't
	// added a host_vars/faas-fsn-{1,2}.yml yet. Allowed values:
	// "", "single-box", "control-plane", "compute-only".
	//
	// When set, --daemon is ignored if it would have widened the
	// subset (--box-role wins; it's the per-box shape, --daemon is
	// the per-rotation scope). When set to a value pkg/pki.RolesForBox
	// does not recognise, init/rotate exit 1 with a clear error
	// rather than silently issuing the full Roles() set (the
	// fail-closed posture — see pkg/pki/pki.go:RolesForBox).
	boxRole string
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
	fs.StringVar(&f.boxRole, "box-role", "",
		"per-box PKI subset (Gate-B PR-3): '', 'single-box' = full Roles(); 'control-plane' or 'compute-only' = role-specific leaves (schedd is shared in M9)")
	fs.StringVar(&f.nodeCN, "cn", "",
		"compute node identity for vmmd leaves (compute-only; .faas is appended when omitted)")
	fs.StringVar(&f.transportSAN, "transport-san", "",
		"private DNS name or IP to add to every selected leaf (for example fsn-3.gregale.dev)")
	// --json is wired in the leaf-specific cmd functions so the
	// help text is colocated with the per-leaf intent; the
	// newPKIFlags helper is shared by the destructive leaves
	// (init / rotate) which don't accept --json.
	return fs, f
}

// cmdPKIInit issues the CA + every per-daemon leaf. Idempotent: leaves
// with NotAfter >= ReissueThreshold are skipped silently so re-running
// `gregalectl pki init` after a partial failure doesn't churn the rest of
// the fleet. Pass --force to re-issue unconditionally.
func cmdPKIInit(args []string) int {
	fs, f := newPKIFlags("pki init", false)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregalectl pki init [flags]", "pki")
		return 1
	}
	identity, err := pkiIdentity(f)
	if err != nil {
		return printErr("pki init: identity", err)
	}
	caCert, caKey, err := pki.EnsureCA(f.rootDir, f.force)
	if err != nil {
		return printErr("pki init: ensure CA", err)
	}
	written, skipped, errs := ensureAllLeavesWithIdentity(f.rootDir, caCert, caKey, f.force, f.boxRole, identity.nodeCN, identity.transportSAN)
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
		PrintUsage(os.Stderr, "usage: gregalectl pki status [flags]", "pki")
		return 1
	}
	if pki.HasPendingInstall(f.rootDir) {
		fmt.Fprintln(os.Stderr, "gregalectl pki status: interrupted certificate install requires recovery")
		return 1
	}
	reportCAStatus(os.Stdout, f.rootDir)
	reportLeafStatusAll(os.Stdout, f.rootDir, f.boxRole)
	identity, err := pkiIdentity(f)
	if err != nil {
		return printErr("pki status: identity", err)
	}
	if f.boxRole == "compute-only" && identity.nodeCN == "" {
		identity.nodeCN = inferComputeNodeCN(f.rootDir)
	}
	if err := pki.ValidateTrustBundleForNode(f.rootDir, f.boxRole, identity.transportSAN, identity.nodeCN); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl pki status: invalid or partial trust bundle: %v\n", err)
		return 1
	}
	// Cheap "any expiring within threshold" gate so operators can use
	// this in CI / cron to surface the rotate countdown. Exit 1 here is
	// non-fatal for the human reader but useful as a Nagios-style
	// alarm signal.
	if anyExpiringSoonForBox(f.rootDir, f.boxRole, pki.ReissueThreshold) {
		fmt.Fprintf(os.Stderr, "gregalectl pki status: at least one leaf expires within %s — run `gregalectl pki init` or `gregalectl pki rotate`\n",
			pki.ReissueThreshold)
		return 1
	}
	return 0
}

func cmdPKIIssueBundle(args []string) int {
	fs, f := newPKIFlags("pki issue-bundle", false)
	issuerRoot := fs.String("issuer-root", pki.DefaultRootDir, "operator PKI root containing the CA private key")
	activeRoot := fs.String("active-root", "", "exported active trust bundle used to preserve safe leaves")
	outputDir := fs.String("output-dir", "", "destination for the trust-only host bundle")
	changedFile := fs.String("changed-file", "", "optional JSON output listing renewed leaf roles")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *outputDir == "" || f.boxRole == "" {
		PrintUsage(os.Stderr, "usage: gregalectl pki issue-bundle --issuer-root DIR --output-dir DIR --box-role ROLE [--cn NODE] [--transport-san SAN]", "pki")
		return 1
	}
	identity, err := pkiIdentity(f)
	if err != nil {
		return printErr("pki issue-bundle: identity", err)
	}
	var changed []pki.Role
	if *activeRoot == "" {
		if err := pki.IssueTrustBundle(*issuerRoot, *outputDir, f.boxRole, identity.nodeCN, identity.transportSAN); err != nil {
			return printErr("pki issue-bundle", err)
		}
		changed = pki.RolesForBox(f.boxRole)
	} else {
		changed, err = pki.RenewTrustBundle(*issuerRoot, *activeRoot, *outputDir, f.boxRole, identity.nodeCN, identity.transportSAN)
		if err != nil {
			return printErr("pki issue-bundle", err)
		}
	}
	if *changedFile != "" {
		body, marshalErr := json.Marshal(struct {
			Changed []pkiChangedLeaf `json:"changed"`
		}{Changed: changedLeafShapes(changed)})
		if marshalErr != nil {
			return printErr("pki issue-bundle: marshal changed leaves", marshalErr)
		}
		body = append(body, '\n')
		if writeErr := writePKIMetricsAtomic(*changedFile, body); writeErr != nil {
			return printErr("pki issue-bundle: write changed leaves", writeErr)
		}
	}
	PrintOK(os.Stdout, "Issued trust-only %s bundle at %s (%d leaves renewed)", f.boxRole, *outputDir, len(changed))
	return 0
}

type pkiChangedLeaf struct {
	Directory string `json:"directory"`
	Filename  string `json:"filename"`
}

func changedLeafShapes(roles []pki.Role) []pkiChangedLeaf {
	out := make([]pkiChangedLeaf, 0, len(roles))
	for _, role := range roles {
		out = append(out, pkiChangedLeaf{Directory: role.Directory, Filename: role.Filename})
	}
	return out
}

func cmdPKIExportBundle(args []string) int {
	fs, f := newPKIFlags("pki export-bundle", false)
	sourceRoot := fs.String("source-root", pki.DefaultRootDir, "active trust root to export")
	outputDir := fs.String("output-dir", "", "fresh trust-only export destination")
	forRenewal := fs.Bool("for-renewal", false, "allow repairable active-leaf drift for issuer-side renewal")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *outputDir == "" || f.boxRole == "" {
		PrintUsage(os.Stderr, "usage: gregalectl pki export-bundle --source-root DIR --output-dir DIR --box-role ROLE [--cn NODE] [--transport-san SAN] [--for-renewal]", "pki")
		return 1
	}
	identity, err := pkiIdentity(f)
	if err != nil {
		return printErr("pki export-bundle: identity", err)
	}
	if *forRenewal {
		err = pki.ExportTrustBundleForRenewal(*sourceRoot, *outputDir, f.boxRole)
	} else {
		err = pki.ExportTrustBundle(*sourceRoot, *outputDir, f.boxRole, identity.nodeCN, identity.transportSAN)
	}
	if err != nil {
		return printErr("pki export-bundle", err)
	}
	PrintOK(os.Stdout, "Exported trust-only %s bundle to %s", f.boxRole, *outputDir)
	return 0
}

func cmdPKIFingerprint(args []string) int {
	fs := flag.NewFlagSet("pki fingerprint", flag.ContinueOnError)
	cert := fs.String("cert", "", "certificate path")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *cert == "" {
		PrintUsage(os.Stderr, "usage: gregalectl pki fingerprint --cert FILE", "pki")
		return 1
	}
	fingerprint, err := pki.LoadCertificateFingerprint(*cert)
	if err != nil {
		return printErr("pki fingerprint", err)
	}
	if _, err := fmt.Fprintln(os.Stdout, fingerprint); err != nil {
		return printErr("pki fingerprint", err)
	}
	return 0
}

func cmdPKIInstallBundle(args []string) int {
	fs, f := newPKIFlags("pki install-bundle", false)
	bundleDir := fs.String("bundle-dir", "", "validated trust-only host bundle to install")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *bundleDir == "" || f.boxRole == "" {
		PrintUsage(os.Stderr, "usage: gregalectl pki install-bundle --bundle-dir DIR --root-dir DIR --box-role ROLE [--cn NODE] [--transport-san SAN]", "pki")
		return 1
	}
	identity, err := pkiIdentity(f)
	if err != nil {
		return printErr("pki install-bundle: identity", err)
	}
	if err := pki.InstallTrustBundle(*bundleDir, f.rootDir, f.boxRole, identity.nodeCN, identity.transportSAN); err != nil {
		return printErr("pki install-bundle", err)
	}
	PrintOK(os.Stdout, "Installed %s PKI bundle from %s", f.boxRole, *bundleDir)
	return 0
}

func cmdPKIRecoverInstall(args []string) int {
	fs := flag.NewFlagSet("pki recover-install", flag.ContinueOnError)
	rootDir := fs.String("root-dir", pki.DefaultRootDir, "active PKI root")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregalectl pki recover-install [--root-dir DIR]", "pki")
		return 1
	}
	if err := pki.RecoverPendingInstall(*rootDir); err != nil {
		return printErr("pki recover-install", err)
	}
	PrintOK(os.Stdout, "Recovered any pending PKI install at %s", *rootDir)
	return 0
}

type pkiRenewalState struct {
	LastSuccessUnix int64 `json:"last_success_unix"`
	LastFailureUnix int64 `json:"last_failure_unix"`
	Partial         bool  `json:"partial"`
}

func cmdPKIMetrics(args []string) int {
	fs := flag.NewFlagSet("pki metrics", flag.ContinueOnError)
	rootDir := fs.String("root-dir", pki.DefaultRootDir, "active PKI root")
	boxRole := fs.String("box-role", "", "host role: control-plane or compute-only")
	host := fs.String("host", "", "bounded host label")
	output := fs.String("output", "", "node_exporter textfile destination (stdout when empty)")
	renewalState := fs.String("renewal-state", "/var/lib/faas/pki-renewal/status.json", "renewal workflow state file")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *boxRole == "" || *host == "" {
		PrintUsage(os.Stderr, "usage: gregalectl pki metrics --box-role ROLE --host HOST [--root-dir DIR] [--output FILE] [--renewal-state FILE]", "pki")
		return 1
	}
	body, err := renderPKIMetrics(*rootDir, *boxRole, *host, *renewalState, time.Now())
	if err != nil {
		return printErr("pki metrics", err)
	}
	if *output == "" {
		_, err = os.Stdout.Write(body)
	} else {
		err = writePKIMetricsAtomic(*output, body)
	}
	if err != nil {
		return printErr("pki metrics: write", err)
	}
	return 0
}

func renderPKIMetrics(rootDir, boxRole, host, statePath string, now time.Time) ([]byte, error) {
	roles := pki.RolesForBox(boxRole)
	if len(roles) == 0 {
		return nil, fmt.Errorf("unknown box role %q", boxRole)
	}
	earliest := make(map[string]int64)
	for _, role := range roles {
		certPath, _ := pki.LeafPaths(rootDir, role)
		data, err := os.ReadFile(certPath)
		if err != nil {
			return nil, fmt.Errorf("read %s/%s certificate: %w", role.Directory, role.Filename, err)
		}
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, fmt.Errorf("decode %s/%s certificate: no PEM block", role.Directory, role.Filename)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse %s/%s certificate: %w", role.Directory, role.Filename, err)
		}
		expires := cert.NotAfter.Unix()
		if current, ok := earliest[role.Directory]; !ok || expires < current {
			earliest[role.Directory] = expires
		}
	}

	state := pkiRenewalState{}
	if statePath != "" {
		data, err := os.ReadFile(statePath)
		switch {
		case err == nil:
			if err := json.Unmarshal(data, &state); err != nil {
				return nil, fmt.Errorf("parse renewal state %q: %w", statePath, err)
			}
		case errors.Is(err, os.ErrNotExist):
		default:
			return nil, fmt.Errorf("read renewal state %q: %w", statePath, err)
		}
	}
	daemons := make([]string, 0, len(earliest))
	for daemon := range earliest {
		daemons = append(daemons, daemon)
	}
	sort.Strings(daemons)
	labels := fmt.Sprintf("host=%s,box_role=%s", strconv.Quote(host), strconv.Quote(boxRole))
	var body strings.Builder
	body.WriteString("# HELP faas_internal_mtls_leaf_earliest_expiry_timestamp_seconds Earliest NotAfter among active internal mTLS leaves for a host daemon.\n")
	body.WriteString("# TYPE faas_internal_mtls_leaf_earliest_expiry_timestamp_seconds gauge\n")
	for _, daemon := range daemons {
		fmt.Fprintf(&body, "faas_internal_mtls_leaf_earliest_expiry_timestamp_seconds{%s,daemon=%s} %d\n", labels, strconv.Quote(daemon), earliest[daemon])
	}
	body.WriteString("# HELP faas_internal_mtls_expiry_metrics_last_success_timestamp_seconds Last successful read of every active internal mTLS leaf.\n")
	body.WriteString("# TYPE faas_internal_mtls_expiry_metrics_last_success_timestamp_seconds gauge\n")
	fmt.Fprintf(&body, "faas_internal_mtls_expiry_metrics_last_success_timestamp_seconds{%s} %d\n", labels, now.Unix())
	body.WriteString("# HELP faas_internal_mtls_renewal_last_success_timestamp_seconds Last successful scheduled internal mTLS renewal check.\n")
	body.WriteString("# TYPE faas_internal_mtls_renewal_last_success_timestamp_seconds gauge\n")
	fmt.Fprintf(&body, "faas_internal_mtls_renewal_last_success_timestamp_seconds{%s} %d\n", labels, state.LastSuccessUnix)
	body.WriteString("# HELP faas_internal_mtls_renewal_last_failure_timestamp_seconds Last failed scheduled internal mTLS renewal attempt.\n")
	body.WriteString("# TYPE faas_internal_mtls_renewal_last_failure_timestamp_seconds gauge\n")
	fmt.Fprintf(&body, "faas_internal_mtls_renewal_last_failure_timestamp_seconds{%s} %d\n", labels, state.LastFailureUnix)
	body.WriteString("# HELP faas_internal_mtls_renewal_partial Whether the last scheduled internal mTLS renewal left a host in a partial state.\n")
	body.WriteString("# TYPE faas_internal_mtls_renewal_partial gauge\n")
	partial := 0
	if state.Partial {
		partial = 1
	}
	fmt.Fprintf(&body, "faas_internal_mtls_renewal_partial{%s} %d\n", labels, partial)
	return []byte(body.String()), nil
}

func writePKIMetricsAtomic(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".pki-metrics-")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// cmdPKIRotate re-issues every leaf unconditionally. Equivalent to
// `gregalectl pki init --force`. The CLI splits them so the operator's
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
		PrintUsage(os.Stderr, "usage: gregalectl pki rotate [flags]", "pki")
		return 1
	}
	identity, err := pkiIdentity(f)
	if err != nil {
		return printErr("pki rotate: identity", err)
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
	written, _, errs := ensureAllLeavesFilteredWithIdentity(f.rootDir, caCert, caKey, true, f.daemon, f.boxRole, identity.nodeCN, identity.transportSAN)
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

// rotateRestartHint returns the recommended reload command an
// operator should run after a rotation. ADR-052 §5 / PR-E made
// schedd, vmmd, apid pick up rotated material on a SIGHUP — the
// canonical reload verb is `systemctl kill -s HUP faas-<d>` (the
// PR-E slice wires WatchTLSReload to SIGHUP-driven reload; the
// daemons that don't yet have rotation wiring fall through to
// `systemctl reload` per the legacy behaviour). `kill -s HUP` does
// not restart the daemon; it only fires the signal — schedd +
// vmmd + apid pick up the new material on the next handshake,
// no service interruption.
//
// The whole-fleet list is post-ADR-070 (gatewayd-internal is the
// only gatewayd-named daemon; gatewayd-public uses certmagic, not
// the control-plane PKI). Scoping to a single daemon narrows the
// list so a partial rotation (--daemon egress) only restarts the
// daemons that actually loaded the rotated leaves.
func rotateRestartHint(daemon string) string {
	if daemon != "" {
		switch daemon {
		case "egress":
			// The egress server leaf is consumed by
			// gatewayd-internal; the egress-client leaf is
			// consumed by meterd. Neither daemon has
			// SIGHUP-driven rotation yet (deferred to the
			// Tier A10 cluster), so the hint keeps the
			// legacy `systemctl reload` verb.
			return "Reload: systemctl reload faas-gatewayd-internal faas-meterd  (egress leaves: /etc/faas/tls/egress/egress.crt, /etc/faas/tls/meterd/egress-client.crt)\n   Tip: prefer `systemctl kill -s HUP faas-<daemon>` once the Tier A10 cluster ships PR-E rotation to these daemons."
		case "meterd":
			return "Reload: systemctl reload faas-meterd (rotation PR-E deferred to Tier A10)"
		case "schedd":
			return "Reload (no restart): systemctl kill -s HUP faas-schedd   # picks up the new leaf on the next TLS handshake"
		case "vmmd":
			return "Reload (no restart): systemctl kill -s HUP faas-vmmd   # picks up the new leaf on the next TLS handshake"
		case "apid":
			return "Reload (no restart): systemctl kill -s HUP faas-apid   # picks up the new leaf on the next TLS handshake"
		case "githubd":
			return "Reload: systemctl reload faas-githubd (rotation PR-E deferred to Tier A10)"
		case "builderd":
			return "Reload: systemctl reload faas-builderd (rotation PR-E deferred to Tier A10)"
		}
	}
	return "Reload (no restart): systemctl kill -s HUP faas-{schedd,vmmd,apid}\n" +
		"   Other daemons (gatewayd-internal, meterd, githubd, builderd): systemctl reload faas-<daemon>  # PR-E rotation deferred to Tier A10"
}

type pkiIdentityOptions struct {
	nodeCN       string
	transportSAN pki.AltNames
}

func pkiIdentity(f *pkiFlags) (pkiIdentityOptions, error) {
	identity := pkiIdentityOptions{}
	if f.nodeCN == "" && f.transportSAN == "" {
		return identity, nil
	}
	if f.boxRole != "compute-only" {
		return identity, errors.New("--cn/--transport-san require --box-role=compute-only")
	}
	if raw := strings.TrimSpace(f.nodeCN); raw != "" {
		identity.nodeCN = raw
		if !strings.HasSuffix(identity.nodeCN, ".faas") {
			identity.nodeCN += ".faas"
		}
	}
	if raw := strings.TrimSpace(f.transportSAN); raw != "" {
		if ip := net.ParseIP(raw); ip != nil {
			identity.transportSAN.IPAddresses = []net.IP{ip}
		} else {
			identity.transportSAN.DNSNames = []string{raw}
		}
	}
	return identity, nil
}

func ensureAllLeavesWithIdentity(rootDir string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, force bool, boxRole, nodeCN string, extraSANs pki.AltNames) (int, int, []error) {
	roles := pki.RolesForBox(boxRole)
	var written, skipped int
	var errs []error
	for _, role := range roles {
		err := ensureLeafWithIdentity(rootDir, role, caCert, caKey, force, nodeCN, extraSANs)
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

// ensureAllLeavesFilteredWithIdentity iterates every role, with an optional
// --daemon scope. daemon="<dir>" matches role.Directory == dir PLUS one cross-directory
// carve-out for the egress pair: when daemon=="egress", the meterd
// client leaf (Directory=meterd, Filename=egress-client) is also
// included because the egress server's CN (egress.faas) is paired with
// that client cert at mTLS handshake time — rotating only the server
// leaf would leave meterd holding a stale client cert and every
// subsequent egress dial would fail with a tls: bad certificate.
//
// boxRole is the Gate-B per-box subset filter (--box-role on the CLI).
// When boxRole is non-empty, only the leaves whose Directory survives
// the per-box filter are eligible for the --daemon narrowing; this is
// how `gregalectl pki rotate --box-role=compute-only --daemon=imaged`
// would only rotate the imaged server leaf on fsn-2.
func ensureAllLeavesFilteredWithIdentity(rootDir string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, force bool, daemon, boxRole, nodeCN string, extraSANs pki.AltNames) (int, int, []error) {
	if daemon == "" {
		return ensureAllLeavesWithIdentity(rootDir, caCert, caKey, force, boxRole, nodeCN, extraSANs)
	}
	roles := pki.RolesForBox(boxRole)
	var written, skipped int
	var errs []error
	for _, role := range roles {
		if !roleMatchesDaemon(role, daemon) {
			continue
		}
		err := ensureLeafWithIdentity(rootDir, role, caCert, caKey, force, nodeCN, extraSANs)
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

func ensureLeafWithIdentity(rootDir string, role pki.Role, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, force bool, nodeCN string, extraSANs pki.AltNames) error {
	// vmmd and gatewayd's apid client both connect to the control plane as
	// a compute-node identity. Keep the node CN limited to those client
	// leaves; gatewayd's listener and its other client leaves retain their
	// daemon-role CNs for handler-layer authorization.
	if nodeCN != "" && pki.RoleUsesNodeIdentity(role) {
		return pki.EnsureLeafWithCNAndSANs(rootDir, role, nodeCN, caCert, caKey, force, extraSANs)
	}
	if len(extraSANs.DNSNames) != 0 || len(extraSANs.IPAddresses) != 0 {
		return pki.EnsureLeafWithSANs(rootDir, role, caCert, caKey, force, extraSANs)
	}
	return pki.EnsureLeaf(rootDir, role, caCert, caKey, force)
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

// reportLeafStatusAll prints one line per leaf in
// pki.RolesForBox(boxRole) (or pki.Roles() when boxRole==""). Mirrors
// ensureAllLeaves so `gregalectl pki status` on fsn-2 only walks the
// leaves that box actually owns.
func reportLeafStatusAll(w io.Writer, rootDir, boxRole string) {
	for _, role := range pki.RolesForBox(boxRole) {
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

func anyExpiringSoonForBox(rootDir, boxRole string, threshold time.Duration) bool {
	now := time.Now()
	for _, role := range pki.RolesForBox(boxRole) {
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

func inferComputeNodeCN(rootDir string) string {
	for _, role := range pki.RolesForBox("compute-only") {
		if !pki.RoleUsesNodeIdentity(role) {
			continue
		}
		certPath, _ := pki.LeafPaths(rootDir, role)
		body, err := os.ReadFile(certPath)
		if err != nil {
			continue
		}
		block, _ := pem.Decode(body)
		if block == nil {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err == nil && cert.Subject.CommonName != "" {
			return cert.Subject.CommonName
		}
	}
	return ""
}

// isErrLeafNotExpiringSoon matches the sentinel without using errors.Is
// (the sentinel may be wrapped by EnsureLeaf). String compare is fine
// here because the sentinel's text is stable.
func isErrLeafNotExpiringSoon(err error) bool {
	return err != nil && strings.Contains(err.Error(), pki.ErrLeafNotExpiringSoon.Error())
}

// cmdPKIList is the read-only introspection leaf (Cluster C2 of
// the gregalectl mega-PR). Mirrors `status` but emits the wire
// shape that CI gates + ad-hoc jq pipelines want:
//
//	{box_role, daemon, leaves:[{directory, filename, mode,
//	  serial, not_after, cn, sans, present, path}],
//	 ca:{path, mode, serial, not_after, present}}
//
// Missing leaves / CA stay present=false (other fields zero) so
// the pre-init wire shape is stable. --daemon narrows the leaves
// list to one directory (with the egress cross-directory carve-out,
// identical to the rotate path) so operators can dump a single
// subsystem without churning through the 9+ directory list. The
// leaf never writes (--force is ignored); an exit-1 gate on
// expiry belongs to status, not list.
func cmdPKIList(args []string) int {
	fs := flag.NewFlagSet("pki list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	f := &pkiFlags{}
	fs.StringVar(&f.rootDir, "root-dir", pki.DefaultRootDir,
		"directory under which CA + per-daemon leaves live (canonical: "+pki.DefaultRootDir+")")
	fs.StringVar(&f.daemon, "daemon", "",
		"narrow to one directory (e.g. --daemon egress incl. the meterd/client cross-dir carve-out)")
	fs.StringVar(&f.boxRole, "box-role", "",
		"per-box PKI subset ('' = full Roles(); 'single-box'/'control-plane'/'compute-only' per Gate-B; schedd is shared in M9)")
	jsonOut := fs.Bool("json", false, "emit structured JSON to stdout")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "gregalectl pki list [--daemon NAME] [--box-role ROLE] [--root-dir DIR] [--json]", "pki")
		return 1
	}
	rep := inspectPKI(f.rootDir, f.daemon, f.boxRole)
	if *jsonOut {
		return emitPKIListJSON(osStdout, rep)
	}
	reportCAStatus(os.Stdout, f.rootDir)
	reportLeafStatusFiltered(os.Stdout, f.rootDir, f.daemon, f.boxRole)
	return 0
}

// pkiListReport is the wire shape for `pki list --json`. The
// fields are pinned by json_parity_test so CI gates can rely on
// the schema. daemon is "" when no filter is set (mirror the
// CLI shape; gates can branch on daemon == "" vs. != "").
type pkiListReport struct {
	BoxRole string         `json:"box_role"`
	Daemon  string         `json:"daemon"`
	CA      pkiFileStatus  `json:"ca"`
	Leaves  []pkiLeafShape `json:"leaves"`
}

// pkiFileStatus is the per-file shape used by both the CA and
// every leaf entry. present=false on missing files; otherwise
// mode / serial / not_after are populated and the path echoes
// the on-disk location for diagnostic purposes.
type pkiFileStatus struct {
	Present  bool   `json:"present"`
	Path     string `json:"path"`
	Mode     string `json:"mode,omitempty"`
	Serial   string `json:"serial,omitempty"`
	NotAfter string `json:"not_after,omitempty"`
}

// pkiLeafShape adds the role identity (directory + filename) +
// the parsed cert's CN + SANs on top of pkiFileStatus. The CA
// entry uses pkiFileStatus directly (no role identity).
type pkiLeafShape struct {
	Directory string `json:"directory"`
	Filename  string `json:"filename"`
	pkiFileStatus
	CN   string `json:"cn,omitempty"`
	SANs string `json:"sans,omitempty"`
}

// inspectPKI builds the read-only report. daemon=="" means
// every leaf for the boxRole subset; daemon=="<dir>" uses the
// same carve-out the rotate path uses (roleMatchesDaemon
// below) so list and rotate agree on "what does --daemon X
// cover?". Missing files are reported present=false (other
// fields zero) so the pre-init wire shape is stable.
func inspectPKI(rootDir, daemon, boxRole string) pkiListReport {
	rep := pkiListReport{BoxRole: boxRole, Daemon: daemon}

	// CA
	caCertPath, _ := pki.CARoot(rootDir)
	rep.CA = inspectPKIFile(caCertPath)

	// Leaves
	for _, role := range pki.RolesForBox(boxRole) {
		if daemon != "" && !roleMatchesDaemon(role, daemon) {
			continue
		}
		certPath, _ := pki.LeafPaths(rootDir, role)
		leaf := pkiLeafShape{Directory: role.Directory, Filename: role.Filename}
		leaf.pkiFileStatus = inspectPKIFile(certPath)
		if leaf.Present {
			// Re-parse the bytes already loaded so we can
			// surface CN + SANs in the JSON shape. We
			// re-read once more rather than threading the
			// bytes through inspectPKIFile so the helper
			// stays narrowly typed for the missing-file
			// shape.
			if data, err := os.ReadFile(certPath); err == nil {
				if block, _ := pem.Decode(data); block != nil {
					if cert, certErr := x509.ParseCertificate(block.Bytes); certErr == nil {
						leaf.CN = cert.Subject.CommonName
						leaf.SANs = formatSANs(cert.DNSNames, cert.IPAddresses)
					}
				}
			}
		}
		rep.Leaves = append(rep.Leaves, leaf)
	}
	return rep
}

// inspectPKIFile reads mode + serial + not_after for one
// cert file. Missing files return present=false (other fields
// zero). The shape is mirrored by reportOneStatus for the text
// renderer so the two paths never disagree on what's on disk.
func inspectPKIFile(path string) pkiFileStatus {
	info, err := os.Stat(path)
	if err != nil {
		return pkiFileStatus{Path: path}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return pkiFileStatus{Path: path, Mode: fmt.Sprintf("%#o", info.Mode().Perm())}
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return pkiFileStatus{Path: path, Mode: fmt.Sprintf("%#o", info.Mode().Perm()), Present: true}
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return pkiFileStatus{Path: path, Mode: fmt.Sprintf("%#o", info.Mode().Perm()), Present: true}
	}
	return pkiFileStatus{
		Present:  true,
		Path:     path,
		Mode:     fmt.Sprintf("%#o", info.Mode().Perm()),
		Serial:   cert.SerialNumber.String(),
		NotAfter: cert.NotAfter.Format(time.RFC3339),
	}
}

// emitPKIListJSON writes the report to w. Kept as a free
// function so tests can drive it with a buffer rather than
// osStdout. The exit code is the function's return value so
// the caller can branch on json.Marshal failure (mirrors the
// other emit*JSON helpers in this package).
func emitPKIListJSON(w io.Writer, rep pkiListReport) int {
	body, err := json.Marshal(rep)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl pki list: marshal json: %v\n", err)
		return 1
	}
	if _, err := w.Write(body); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl pki list: write json: %v\n", err)
		return 1
	}
	_, _ = w.Write([]byte("\n"))
	return 0
}

// reportLeafStatusFiltered is the text counterpart to
// inspectPKI's leaves walk; same --daemon / --box-role
// filter so the two renderers never disagree.
func reportLeafStatusFiltered(w io.Writer, rootDir, daemon, boxRole string) {
	for _, role := range pki.RolesForBox(boxRole) {
		if daemon != "" && !roleMatchesDaemon(role, daemon) {
			continue
		}
		certPath, _ := pki.LeafPaths(rootDir, role)
		label := fmt.Sprintf("%-9s %s", role.Directory, role.Filename)
		reportOneStatus(w, label, certPath)
	}
}
