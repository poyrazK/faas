// Command denylist-md generates docs/denylist.md from the shared
// egress catalog (pkg/netns.NewDefaultDenySet + the OCI-only
// client hardening entries in pkg/oci/egress.go). It is the
// operator artifact promised by ADR-034 §Consequences
// (L149-150: "docs/denylist.md lists every deny line with
// source ADR + rationale + test pin").
//
// Wired into the Makefile's `denylist-md` target and run as
// part of `spec-check`, so a stale catalog edit is caught at
// `git diff --exit-code docs/denylist.md` time.
//
// The output is deterministic: v4 entries sort by canonical
// prefix ascending; v6 entries the same; SMTP ports sort
// numerically. Never iterate over a map without sorting first.
//
// With -customer it renders docs/egress-denylist.md instead: the same
// catalog for the public docs page, without source ADRs, test pins or
// enforcement internals.
//
// Two renderers in one:
//
//   - Platform-wide catalog (NewDefaultDenySet): every entry
//     is enforced by the per-netns forward chain, the host
//     forward chain, AND the OCI dialer. Test pin:
//     pkg/netns/denylist_test.go (provenance + shape) +
//     pkg/netns/denylist_external_test.go (cross-renderer invariant,
//     PR-D).
//   - OCI-only client hardening: the five v4 entries in
//     pkg/oci/egress.go that live ONLY on the OCI dialer (the
//     host firewall does not need them; they are process-level
//     defence-in-depth). Test pin: pkg/oci/egress_test.go.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/oci"
)

// ociOnlyTestPin is the regression net for every OCI-only entry.
// Hard-coded once; the entry set itself is consumed via
// oci.OCIOnlyDenyCIDRsV4() so the table can never drift from
// the runtime enforcement path (PR-D review).
const ociOnlyTestPin = "pkg/oci/egress_test.go::TestIPAllowed_OCIOnlyEntriesDenied"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "denylist-md:", err)
		os.Exit(1)
	}
}

func run() error {
	customer := flag.Bool("customer", false, "render the customer docs page (docs/egress-denylist.md)")
	flag.Parse()
	ds := netns.NewDefaultDenySet()
	ociOnly := oci.OCIOnlyDenyCIDRsV4()
	out := render(ds, ociOnly)
	if *customer {
		out = renderCustomer(ds, ociOnly, api.TenantEgressAllowlistBlockedPorts(), portReason)
	}
	if _, err := os.Stdout.WriteString(out); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}

// render emits the full markdown document. Pure function; no
// timestamps, no maps iterated without sort, no template strings.
// The OCI-only section is sourced from pkg/oci via the exported
// accessor (PR-D review) so the artifact can never drift from
// the runtime enforcement path.
func render(ds netns.DenySet, ociOnly []netns.DenyEntry) string {
	var b []byte

	// Header.
	b = append(b, []byte("# Tenant egress denylist\n\n")...)
	b = append(b, []byte("<!-- GENERATED — do not edit by hand; regenerate with `make denylist-md`. -->\n\n")...)
	b = append(b, []byte("Single source of truth: [`pkg/netns/denylist.go::NewDefaultDenySet()`](pkg/netns/denylist.go).\n")...)
	b = append(b, []byte("The OCI-only section is the typed `ociOnlyDenyCIDRsV4` array in [`pkg/oci/egress.go`](pkg/oci/egress.go).\n\n")...)

	// Section 1: Platform-wide catalog.
	b = append(b, []byte("## Platform-wide catalog\n\n")...)
	b = append(b, []byte("Enforced by all three sinks: per-netns nftables (table `ip faas` / `ip6 faas`, chain `forward`), host nftables (table `inet faas`, chain `forward`), and the OCI user-space dialer. Cross-renderer invariant pinned by `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnDenySet`.\n\n")...)

	// IPv4 rows.
	b = append(b, []byte("### IPv4 CIDRs\n\n")...)
	b = append(b, []byte("| CIDR | Source | Rationale | Test pin |\n")...)
	b = append(b, []byte("|------|--------|-----------|----------|\n")...)
	var v4Rows []denyEntryView
	for _, e := range ds.Entries {
		if e.Family == netns.FamilyV4 {
			v4Rows = append(v4Rows, denyEntryView{Prefix: e.Prefix.String(), SourceADR: e.SourceADR, Comment: e.Comment})
		}
	}
	sort.Slice(v4Rows, func(i, j int) bool { return v4Rows[i].Prefix < v4Rows[j].Prefix })
	for _, r := range v4Rows {
		b = append(b, []byte(fmt.Sprintf("| `%s` | %s | %s | `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnDenySet` |\n",
			r.Prefix, r.SourceADR, r.Comment))...)
	}
	b = append(b, '\n')

	// IPv6 rows.
	b = append(b, []byte("### IPv6 CIDRs\n\n")...)
	b = append(b, []byte("| CIDR | Source | Rationale | Test pin |\n")...)
	b = append(b, []byte("|------|--------|-----------|----------|\n")...)
	var v6Rows []denyEntryView
	for _, e := range ds.Entries {
		if e.Family == netns.FamilyV6 {
			v6Rows = append(v6Rows, denyEntryView{Prefix: e.Prefix.String(), SourceADR: e.SourceADR, Comment: e.Comment})
		}
	}
	sort.Slice(v6Rows, func(i, j int) bool { return v6Rows[i].Prefix < v6Rows[j].Prefix })
	for _, r := range v6Rows {
		b = append(b, []byte(fmt.Sprintf("| `%s` | %s | %s | `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnDenySet` |\n",
			r.Prefix, r.SourceADR, r.Comment))...)
	}
	b = append(b, '\n')

	// SMTP rows.
	b = append(b, []byte("### SMTP TCP ports\n\n")...)
	b = append(b, []byte("| Port | Source | Rationale | Test pin |\n")...)
	b = append(b, []byte("|------|--------|-----------|----------|\n")...)
	smtpPorts := append([]uint16{}, ds.SMTPPorts...)
	sort.Slice(smtpPorts, func(i, j int) bool { return smtpPorts[i] < smtpPorts[j] })
	for _, p := range smtpPorts {
		b = append(b, []byte(fmt.Sprintf("| `%d` | `spec-§11` | spam = Hetzner abuse desk = existential (spec §7 founding doc R6) | `pkg/netns/denylist_test.go::TestNewDefaultDenySet_SMTPPortsAreComplete`, `pkg/netns/denylist_external_test.go::TestAllThreeConsumersAgreeOnSMTPPorts` |\n",
			p))...)
	}
	b = append(b, '\n')

	// Section 2: OCI-only client hardening.
	b = append(b, []byte("## OCI-only client hardening\n\n")...)
	b = append(b, []byte("These ranges are enforced by the OCI user-space dialer ONLY. They are intentionally NOT in the shared catalog because the host firewall does not need them (no tenant process binds to loopback from the OCI puller, etc.). They are process-level defence-in-depth: if the firewall ever regresses, the user-space check still refuses the dial. Pinned by `pkg/oci/egress_test.go`.\n\n")...)
	b = append(b, []byte("Note: the `0.0.0.0/8` and `127.0.0.0/8` entries are also denied by the `IsLoopback` / `IsUnspecified` predicate in `pkg/oci/egress.go::ipAllowed` (the address-class layer). They are restated here as explicit ranges so the user-space check remains a deny even if the predicate is ever refactored. `192.0.0.0/24`, `198.18.0.0/15`, and `240.0.0.0/4` are NOT covered by the predicate and rely on these entries alone.\n\n")...)

	b = append(b, []byte("### IPv4 CIDRs (OCI-only)\n\n")...)
	b = append(b, []byte("| CIDR | Source | Rationale | Test pin |\n")...)
	b = append(b, []byte("|------|--------|-----------|----------|\n")...)
	// Sort by canonical prefix string so the diff stays reviewable.
	// The entries are typed netns.DenyEntry — SourceADR and Comment
	// are read directly from the accessor, no second source of truth.
	// The IsLoopback/IsUnspecified predicate in pkg/oci/egress.go
	// also denies these ranges at the address-class layer; the
	// entries here are explicit defence-in-depth so the user-space
	// check remains a deny even if the predicate is ever refactored.
	ocr := make([]denyEntryView, 0, len(ociOnly))
	for _, e := range ociOnly {
		ocr = append(ocr, denyEntryView{Prefix: e.Prefix.String(), SourceADR: e.SourceADR, Comment: e.Comment})
	}
	sort.Slice(ocr, func(i, j int) bool { return ocr[i].Prefix < ocr[j].Prefix })
	for _, r := range ocr {
		b = append(b, []byte(fmt.Sprintf("| `%s` | %s | %s | `%s` |\n",
			r.Prefix, r.SourceADR, r.Comment, ociOnlyTestPin))...)
	}

	return string(b)
}

type denyEntryView struct {
	Prefix    string
	SourceADR string
	Comment   string
}

func portReason(port int) string {
	reason, _ := api.TenantEgressForbiddenPort(port)
	return reason
}

// customerPortReasons rewords pkg/api's forbidden-port reasons for
// customers. A reason missing here is printed as it is.
var customerPortReasons = map[string]string{
	"SMTP":                                   "Outbound mail (SMTP). Send mail through your provider's HTTPS API instead.",
	"remote administration":                  "Remote administration (SSH, Telnet, RDP, VNC)",
	"Windows RPC/SMB":                        "Windows RPC and file sharing (SMB)",
	"IRC":                                    "IRC",
	"cryptocurrency mining":                  "Cryptocurrency mining pools",
	"DNS is pinned to the platform resolver": "DNS and DNS over TLS. Name lookups always go to Gregale's resolver.",
}

// customerRangeNote returns a deny entry's comment for customers: the part
// before the first ";" (the rest is threat-model detail), capitalized.
func customerRangeNote(e netns.DenyEntry) string {
	if e.Prefix.String() == "169.254.0.0/16" {
		return "Link-local, including the cloud metadata address `169.254.169.254`"
	}
	note, _, _ := strings.Cut(e.Comment, ";")
	note = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(note), "(defence-in-depth)"))
	if note == "" {
		return note
	}
	return strings.ToUpper(note[:1]) + note[1:]
}

// renderCustomer emits docs/egress-denylist.md. Pure and deterministic, like
// render. blockedPorts are the ports closed even to allowlisted destinations;
// reason names each one's category.
func renderCustomer(ds netns.DenySet, ociOnly []netns.DenyEntry, blockedPorts []int, reason func(int) string) string {
	var b strings.Builder
	b.WriteString("# Blocked outbound destinations\n\n")
	b.WriteString("<!-- GENERATED — do not edit by hand; regenerate with `make denylist-md`. -->\n\n")
	b.WriteString("Apps can't open connections to the addresses and ports on this page. The\n")
	b.WriteString("rules apply to every app on every plan, and an egress allowlist entry does not\n")
	b.WriteString("open them. See [Networking](networking.md#outbound-connections) for what an app\n")
	b.WriteString("can reach.\n\n")

	b.WriteString("## Blocked address ranges\n\n")
	for _, family := range []struct {
		name string
		f    netns.Family
	}{{"IPv4", netns.FamilyV4}, {"IPv6", netns.FamilyV6}} {
		var rows []netns.DenyEntry
		for _, e := range ds.Entries {
			if e.Family == family.f {
				rows = append(rows, e)
			}
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Prefix.String() < rows[j].Prefix.String() })
		fmt.Fprintf(&b, "### %s\n\n| Range | What it is |\n|---|---|\n", family.name)
		for _, e := range rows {
			fmt.Fprintf(&b, "| `%s` | %s |\n", e.Prefix, customerRangeNote(e))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Blocked ports\n\n")
	b.WriteString("These TCP ports stay closed, also to destinations on an app's egress\n")
	b.WriteString("allowlist, and `gregale app egress-ports add` refuses them.\n\n")
	b.WriteString("| Ports | What they are |\n|---|---|\n")
	var order []string
	groups := map[string][]string{}
	ports := append([]int{}, blockedPorts...)
	sort.Ints(ports)
	for _, p := range ports {
		r := reason(p)
		if _, ok := groups[r]; !ok {
			order = append(order, r)
		}
		groups[r] = append(groups[r], fmt.Sprintf("`%d`", p))
	}
	for _, r := range order {
		text := r
		if c, ok := customerPortReasons[r]; ok {
			text = c
		}
		fmt.Fprintf(&b, "| %s | %s |\n", strings.Join(groups[r], ", "), text)
	}
	smtp := append([]uint16{}, ds.SMTPPorts...)
	sort.Slice(smtp, func(i, j int) bool { return smtp[i] < smtp[j] })
	var submission []string
	blocked := map[int]bool{}
	for _, p := range ports {
		blocked[p] = true
	}
	for _, p := range smtp {
		if !blocked[int(p)] {
			submission = append(submission, fmt.Sprintf("`%d`", p))
		}
	}
	if len(submission) > 0 {
		fmt.Fprintf(&b, "\nMail submission ports %s are open only to destinations on the app's egress\nallowlist, so an app can send through a mail provider it lists explicitly.\n", strings.Join(submission, " and "))
	}
	b.WriteString("\n")

	b.WriteString("## Image pulls\n\n")
	b.WriteString("When Gregale pulls a container image for a deployment, it also refuses a\n")
	b.WriteString("registry that resolves to one of these ranges:\n\n")
	b.WriteString("| Range | What it is |\n|---|---|\n")
	oci := append([]netns.DenyEntry{}, ociOnly...)
	sort.Slice(oci, func(i, j int) bool { return oci[i].Prefix.String() < oci[j].Prefix.String() })
	for _, e := range oci {
		fmt.Fprintf(&b, "| `%s` | %s |\n", e.Prefix, customerRangeNote(e))
	}
	return b.String()
}
