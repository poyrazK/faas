// Package runtimecheck is the load-bearing test time + startup
// gate for the capdecl package. The check is intentionally
// declarative: pass a Declaration + a /proc/self/status-shaped
// io.Reader, get back a *Violation if anything is off.
//
// The function is split between the runtimecheck.Validate(decl,
// masks) (pure parsing + decisions, no I/O) and
// runtimecheck.Check(decl, opts) (reads /proc/<pid>/status via
// the Options struct). The split lets the unit test feed a
// fixture string and lets production main.go call Check against
// the live process.
//
// The integration test in pkg/capdecl/runtimecheck/runtimecheck_test.go
// runs in `make test-metal` (build-tag `//go:build metal`) so the
// host's actual /proc/self/status drives the check. On macOS dev
// the test runs against an empty mask and the per-daemon cap.go
// self-tests (which call Validate declaratively) are the surface
// that catches a misplaced Allow entry.
package runtimecheck

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/capdecl"
)

// Options configures one Check call. PID is the process whose
// /proc/<pid>/status is read; pass 0 for the current process
// (the default — most callers want to check the daemon's own
// cap set). StatusReader overrides the file read for tests; the
// nil-default path uses os.Open("/proc/<pid>/status").
type Options struct {
	PID          int
	StatusReader io.Reader
}

// Check validates decl against the live cap masks. The function
// returns nil on success. On failure it returns a *Violation
// describing which cap, which mask, and which side of the
// declaration was violated (an Allow-cap that isn't in Bnd, a
// Deny-cap that is in Bnd, or unknown cap names).
//
// The check is intentionally strict: every Allow cap must be
// present in Bnd (the bounding set is the reachable superset;
// Eff is what the process actually uses but the Bnd bit is what
// spec §11 asserts). The Deny list is checked against Bnd too:
// a Deny cap that's in Bnd means the daemon's process is
// potentially able to acquire it — the lint rule + the capdecl
// contract say it should not be.
func Check(decl capdecl.Declaration, opts Options) error {
	if err := decl.Validate(); err != nil {
		return fmt.Errorf("runtimecheck: declaration invalid: %w", err)
	}

	var mask capdecl.CapMasks
	switch {
	case opts.StatusReader != nil:
		buf, err := io.ReadAll(opts.StatusReader)
		if err != nil {
			return fmt.Errorf("runtimecheck: read status: %w", err)
		}
		mask = capdecl.ParseStatus(buf)
	case opts.PID == 0:
		var rerr error
		mask, rerr = readSelfStatus()
		if rerr != nil {
			// Review finding M3: readSelfStatus MUST surface
			// errors instead of returning a zero mask that
			// silently passes every Allow/Deny assertion.
			// The previous shape failed OPEN: a daemon whose
			// /proc/self/status couldn't be read (misconfigured
			// mount namespace, kernel bug, blocked-by-seccomp)
			// would boot with an empty CapMasks, the Check
			// would find no Allow caps missing AND no Deny caps
			// present, and the daemon would proceed with no
			// signal that introspection failed. Fail closed.
			return fmt.Errorf("runtimecheck: read self status: %w", rerr)
		}
	case opts.PID > 0:
		//nolint:forbidigo // DEPLOY-1 / ADR-075: /proc/<pid>/status is kernel-controlled, not customer-supplied; the openCustomerFile path is for CLI tarball ingestion, not runtime capability introspection.
		f, err := procOpen(fmt.Sprintf("/proc/%d/status", opts.PID))
		if err != nil {
			return fmt.Errorf("runtimecheck: open /proc/%d/status: %w", opts.PID, err)
		}
		defer func() { _ = f.Close() }()
		buf, err := io.ReadAll(f)
		if err != nil {
			return fmt.Errorf("runtimecheck: read /proc/%d/status: %w", opts.PID, err)
		}
		mask = capdecl.ParseStatus(buf)
	default:
		return errors.New("runtimecheck: invalid PID")
	}

	if missing, unknown := mask.Has(decl.Allow, mask.Bnd); missing != "" || len(unknown) > 0 {
		return &Violation{
			Kind:    ViolationAllowMissing,
			Caps:    grouped(missing, unknown),
			Mask:    "Bnd",
			MaskVal: mask.Bnd,
			Have:    mask,
			Want:    decl,
		}
	}
	if unexpected, unknown := mask.NotIn(decl.Deny, mask.Bnd); len(unexpected) > 0 || len(unknown) > 0 {
		return &Violation{
			Kind:    ViolationDenyPresent,
			Caps:    groupedFromList(unexpected, unknown),
			Mask:    "Bnd",
			MaskVal: mask.Bnd,
			Have:    mask,
			Want:    decl,
		}
	}
	return nil
}

// Validate is the pure-logic check on already-parsed masks.
// Production main.go calls Check; the unit tests call Validate
// with a fixture-driven mask so the test runs on macOS where
// /proc/<pid>/status doesn't exist.
func Validate(decl capdecl.Declaration, mask capdecl.CapMasks) error {
	if err := decl.Validate(); err != nil {
		return fmt.Errorf("runtimecheck: declaration invalid: %w", err)
	}
	if missing, unknown := mask.Has(decl.Allow, mask.Bnd); missing != "" || len(unknown) > 0 {
		return &Violation{
			Kind:    ViolationAllowMissing,
			Caps:    grouped(missing, unknown),
			Mask:    "Bnd",
			MaskVal: mask.Bnd,
			Have:    mask,
			Want:    decl,
		}
	}
	if unexpected, unknown := mask.NotIn(decl.Deny, mask.Bnd); len(unexpected) > 0 || len(unknown) > 0 {
		return &Violation{
			Kind:    ViolationDenyPresent,
			Caps:    groupedFromList(unexpected, unknown),
			Mask:    "Bnd",
			MaskVal: mask.Bnd,
			Have:    mask,
			Want:    decl,
		}
	}
	return nil
}

// Violation describes one capdecl failure. The Kind + Caps + Mask
// fields let a log message identify the offending cap(s) and the
// name of the mask that no longer matches the declaration.
type Violation struct {
	// Kind is the category of violation.
	Kind ViolationKind
	// Caps is the list of cap names involved, possibly split into
	// "unknown" (not in our Decode table) and the rest. Format
	// matches the rendering in Violation.Error().
	Caps []string
	// Mask is the /proc/self/status mask name that the check
	// inspected (always "Bnd" today).
	Mask string
	// MaskVal is the hex-encoded value of the mask at the time
	// of the check — useful for diffing across two runs.
	MaskVal uint64
	// Have is the parsed live cap set.
	Have capdecl.CapMasks
	// Want is the declaration that was checked.
	Want capdecl.Declaration
}

// ViolationKind categorises a violation. The four values cover
// the four error shapes the runtimecheck can produce.
type ViolationKind int

const (
	// ViolationAllowMissing: an Allow-listed cap is not in the
	// live Bnd set. The daemon is missing a cap it claims to
	// require.
	ViolationAllowMissing ViolationKind = iota + 1
	// ViolationDenyPresent: a Deny-listed cap IS in the live Bnd
	// set. The daemon is potentially able to acquire a cap it
	// promised never to have.
	ViolationDenyPresent
	// ViolationUnknownCap: a cap name in the declaration is not
	// in the Decode table. Could be a typo or a kernel-cap added
	// after we last updated capbits.go.
	ViolationUnknownCap
)

func (v *Violation) Error() string {
	var sb strings.Builder
	switch v.Kind {
	case ViolationAllowMissing:
		sb.WriteString("capdecl: declaration requires caps not in live Bnd set: ")
	case ViolationDenyPresent:
		sb.WriteString("capdecl: declaration denies caps present in live Bnd set: ")
	case ViolationUnknownCap:
		sb.WriteString("capdecl: declaration contains caps unknown to the kernel capset table: ")
	}
	sb.WriteString(strings.Join(v.Caps, " "))
	fmt.Fprintf(&sb, " (mask=%s val=0x%x) have=%s want=%s",
		v.Mask, v.MaskVal, renderMask(&v.Have), v.Want.String())
	return sb.String()
}

// readSelfStatus reads the current process's /proc/self/status
// and parses the cap mask lines. Returns a non-nil error on
// open/read/parse failure — Check MUST fail closed in that case
// (review finding M3: the previous shape returned a zero mask on
// any error, which silently passed every Allow/Deny assertion and
// let misconfigured daemons boot with no introspection signal).
//
// The function is gated to //go:build linux by the package's
// metal integration test (cmd/<daemon>/runtimecheck_test.go); on
// macOS dev, callers MUST use Options.StatusReader (a fixture
// io.Reader) to drive Check. The non-metal dev path never reaches
// readSelfStatus.
func readSelfStatus() (capdecl.CapMasks, error) {
	//nolint:forbidigo // DEPLOY-1 / ADR-075: /proc/self/status is kernel-controlled; the openCustomerFile path is for CLI tarball ingestion, not runtime capability introspection.
	f, err := procOpen(procSelfStatusPath)
	if err != nil {
		return capdecl.CapMasks{}, fmt.Errorf("open %s: %w", procSelfStatusPath, err)
	}
	defer func() { _ = f.Close() }()
	buf, err := io.ReadAll(f)
	if err != nil {
		return capdecl.CapMasks{}, fmt.Errorf("read %s: %w", procSelfStatusPath, err)
	}
	mask := capdecl.ParseStatus(buf)
	// An all-zero mask is valid for a fully capability-free daemon. Detect a
	// missing/malformed proc contract from the source lines themselves rather
	// than treating that valid value as a parse failure.
	if !hasCompleteCapabilityStatus(buf) {
		return capdecl.CapMasks{}, errors.New("parse " + procSelfStatusPath + ": no cap lines found (kernel too old or non-Linux)")
	}
	return mask, nil
}

func hasCompleteCapabilityStatus(buf []byte) bool {
	want := map[string]bool{
		"CapInh": false,
		"CapPrm": false,
		"CapEff": false,
		"CapBnd": false,
		"CapAmb": false,
	}
	for _, line := range strings.Split(string(buf), "\n") {
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		key := line[:colon]
		if _, ok := want[key]; !ok {
			continue
		}
		var value uint64
		if _, err := fmt.Sscanf(line[colon+1:], " %x", &value); err == nil {
			want[key] = true
		}
	}
	for _, present := range want {
		if !present {
			return false
		}
	}
	return true
}

// String is a convenience for log messages. It returns the same
// rendering as Violation.Error() minus the leading "capdecl:"
// prefix.
func (v *Violation) String() string {
	return strings.TrimPrefix(v.Error(), "capdecl: ")
}

func grouped(missing string, unknown []string) []string {
	out := make([]string, 0, 1+len(unknown))
	if missing != "" {
		out = append(out, missing)
	}
	out = append(out, unknown...)
	return out
}

func groupedFromList(unexpected, unknown []string) []string {
	out := make([]string, 0, len(unexpected)+len(unknown))
	out = append(out, unexpected...)
	out = append(out, unknown...)
	return out
}

// renderMask returns a compact hex rendering of the masks, in
// the order Inh/Prm/Eff/Bnd/Amb. Stable across calls so log
// diffing is meaningful.
func renderMask(m *capdecl.CapMasks) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "CapInh=0x%x CapPrm=0x%x CapEff=0x%x CapBnd=0x%x CapAmb=0x%x",
		m.Inh, m.Prm, m.Eff, m.Bnd, m.Amb)
	return sb.String()
}

// ComposeFixture is a small test helper that produces a
// /proc/self/status-style byte slice from a few cap bits. Tests
// use it to build the expected mask without going through the
// real /proc parser twice.
func ComposeFixture(inh, prm, eff, bnd, amb uint64) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Name:\tcompose_fixture\n")
	fmt.Fprintf(&buf, "CapInh:\t%016x\n", inh)
	fmt.Fprintf(&buf, "CapPrm:\t%016x\n", prm)
	fmt.Fprintf(&buf, "CapEff:\t%016x\n", eff)
	fmt.Fprintf(&buf, "CapBnd:\t%016x\n", bnd)
	fmt.Fprintf(&buf, "CapAmb:\t%016x\n", amb)
	return buf.Bytes()
}

// procSelfStatusPath is the file readSelfStatus opens. Tests
// override to a t.TempDir() fixture so the macOS dev path can
// exercise the open/read error branches of readSelfStatus and
// the per-PID open branch of Check. Production keeps the
// default of /proc/self/status.
var procSelfStatusPath = "/proc/self/status"

// procOpen is the file-open function Check uses for the
// `opts.PID > 0` branch (runtimecheck.go:84-89). Defaults to
// os.Open; tests override to inject open errors.
//
//nolint:forbidigo // seam for test-only fault injection; production reads /proc/{PID}/status, not a customer-supplied path, so the openCustomerFile guard does not apply.
var procOpen = os.Open
