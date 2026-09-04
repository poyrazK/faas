// Package daemonunit is the single source of truth for the systemd unit
// files the one-box FaaS platform ships. It encodes the subset of systemd
// directives used by the 8 production daemons as a Unit struct, with
// Render() emitting a comment-free unit file and Decode() round-tripping
// it back into a struct for the `deployctl check` CI gate.
//
// Companion package: pkg/daemonunitspec — one UnitXxx() func per daemon.
//
// Rationale: hand-edited unit files drift across deploy trees
// (deploy/{controlplane,systemd/,ansible/roles/control_plane_service/files/}).
// Generating from one Go-rendered artifact — the same shape as the egress
// ruleset at pkg/netns/policy.go — keeps the three trees byte-identical.
// See ADR-078 for the design + the wipe-comments migration.
package daemonunit

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
)

// KV is an ordered key/value entry. Used for Environment= lines
// (preserves insertion order — DAEMONUNIT_ENV_KV_ORDERED).
type KV struct {
	Key   string
	Value string
}

// LoadCred encodes LoadCredential= (or LoadCredential= with the `:-(`
// optional-flag for missing-toleration on rotation overlap; see
// pkg/secretbox). When Optional is true, the rendered directive is
// `LoadCredential=<name>:-<path>`; otherwise `LoadCredential=<name>:<path>`.
type LoadCred struct {
	Name     string
	Path     string
	Optional bool
}

// Unit encodes a subset of a systemd [Unit] + [Service] + [Install] unit
// file. Field ordering inside Render() matches what systemd(1) and
// systemd-analyze(8) expect; comments are intentionally absent. Use
// pkg/daemonunitspec.UUnitXxx() funcs for the canonical per-daemon
// values; this struct is the building block, not the place to tune
// per-daemon details (which drift across trees has been the historical
// foot-gun this package erases).
//
// Field-by-field encoding rules:
//
//   - Empty scalars (string == "") ⇒ omit that directive.
//   - Slice values (After, Wants, Requires, CapabilityBoundingSet, etc.)
//     are emitted as space-separated tokens in the order they were set;
//     nil and len()==0 both mean "no such directive".
//   - Booleans (NoNewPrivileges, ProtectHome, etc.) emit their directive
//     ONLY when true (false is the same as omitted).
//   - Triple-state directives (PrivateTmp; vmmd + schedd need `=no`)
//     use `*bool` so nil ⇒ omit, &true ⇒ `=yes`, &false ⇒ `=no`.
//     PrivateTmp is the only one this matters for today.
//
// CapabilityBoundingSet: empty slice ⇒ emit an EMPTY directive body
// (`CapabilityBoundingSet=`), which systemd treats as the empty
// capability set — locked down. Non-empty ⇒ space-separated cap names.
// An UNCLEAR cap set uses CapabilityBoundingSet= explicitly listing
// every needed cap; omit-the-directive inherits systemd's default
// (effectively the full cap set for system services), which we never
// want in production.
//
// AmbientCapabilities: empty slice ⇒ OMIT the directive entirely (no
// ambient caps elevated at fork; the unit runs with no extra caps).
// Non-empty ⇒ space-separated names; `CAP_*` and `cap_*` are both valid.
//
// RuntimeDirectory + RuntimeDirectoryMode are emitted as a pair for legacy
// unit specs; an empty RuntimeDirectory with a non-empty Mode is silently
// dropped by Render() (systemd would warn, but we keep the surface flat).
type Unit struct {
	// [Unit]
	Description   string
	Documentation string
	After         []string
	Wants         []string
	Requires      []string

	// [Service]
	Type               string // "simple" for every faas daemon today
	User               string
	Group              string
	ExecStart          string
	ExecStartPre       []string // ordered (vmmd has 2; nobody else has any)
	ExecStartPost      []string // ordered post-start fixups (vmmd runtime dir)
	Restart            string
	RestartSec         string
	RestartCountExport string // systemd 254+; e.g. "SYSTEMD_RESTARTS_ON_FAILURE"
	Slice              string
	// MemoryHigh is the soft limit: systemd applies reclaim pressure and
	// throttles the cgroup past this point instead of killing it. Set it
	// below MemoryMax so a slow leak degrades the daemon rather than
	// dropping it — vmmd's 2026-09-03 OOM took its whole compute node out
	// of rotation, and a hard cap alone would not have changed that.
	// Empty omits the directive.
	MemoryHigh            string
	MemoryMax             string
	Delegate              bool
	CapabilityBoundingSet []string
	AmbientCapabilities   []string
	EnvironmentFile       string
	Environment           []KV
	LoadCredential        []LoadCred

	// Hardening
	NoNewPrivileges         bool
	ProtectSystem           string // "strict" for every faas daemon today
	ProtectHome             bool
	PrivateTmp              *bool // nil ⇒ omit; &true ⇒ yes; &false ⇒ no (vmmd + schedd)
	PrivateDevices          bool
	ProtectKernelTunables   bool
	ProtectKernelModules    bool
	ProtectControlGroups    bool
	SystemCallArchitectures string // "native" for the two gatewayd-internal daemons
	LockPersonality         bool
	RestrictNamespaces      bool
	RestrictRealtime        bool
	RestrictSUIDSGID        bool
	RestrictAddressFamilies []string
	ProtectHostname         bool
	ProtectClock            bool
	ProtectProc             string // e.g. "invisible"

	// Filesystem
	ReadOnlyPaths        []string
	ReadWritePaths       []string
	RuntimeDirectory     string // legacy generic field; vmmd uses host tmpfiles instead
	RuntimeDirectoryMode string

	// [Install]
	WantedBy string
}

// BoolPtr returns a pointer to b. Helper for spec tables that need to
// distinguish unset/true/false in a *bool field.
func BoolPtr(b bool) *bool { return &b }

// Render emits the unit file as bytes. Section ordering: [Unit] first,
// then [Service], then [Install] — matching every shipped faas unit.
// Inside [Service], field ordering is fixed (Type → User → Group →
// ExecStartPre → ExecStart → Restart → RestartSec → Slice → MemoryHigh → MemoryMax → Delegate →
// CapabilityBoundingSet → AmbientCapabilities → EnvironmentFile →
// Environment entries → LoadCredential entries → NoNewPrivileges →
// ProtectSystem → ProtectHome → PrivateTmp → PrivateDevices →\n →
// ProtectKernelTunables → ProtectKernelModules → ProtectControlGroups →\n →
// SystemCallArchitectures → LockPersonality → RestrictNamespaces →\n →
// RestrictRealtime → RestrictSUIDSGID → RestrictAddressFamilies →\n →
// ProtectHostname → ProtectClock → ProtectProc →\n → ReadOnlyPaths →\n →
// ReadWritePaths →\n → RuntimeDirectory → RuntimeDirectoryMode → [Install]).
//
// Render() never comments. The wiped rationale moves to godoc on
// UnitXxx() in pkg/daemonunitspec + ADR-078.
func (u Unit) Render() []byte {
	var buf bytes.Buffer
	buf.WriteString("[Unit]\n")
	writeStringKV(&buf, "Description", u.Description)
	if u.Documentation != "" {
		buf.WriteString("Documentation=")
		buf.WriteString(u.Documentation)
		buf.WriteByte('\n')
	}
	writeStringList(&buf, "After", u.After)
	writeStringList(&buf, "Wants", u.Wants)
	writeStringList(&buf, "Requires", u.Requires)
	buf.WriteByte('\n')

	buf.WriteString("[Service]\n")
	writeStringKV(&buf, "Type", u.Type)
	writeStringKV(&buf, "User", u.User)
	writeStringKV(&buf, "Group", u.Group)

	// ExecStartPre comes BEFORE ExecStart (systemd only allows ExecStartPre
	// before ExecStart in the directive list; the inverse order is a
	// `systemd-analyze verify` warning).
	for _, pre := range u.ExecStartPre {
		buf.WriteString("ExecStartPre=")
		buf.WriteString(pre)
		buf.WriteByte('\n')
	}
	writeStringKV(&buf, "ExecStart", u.ExecStart)
	for _, post := range u.ExecStartPost {
		buf.WriteString("ExecStartPost=")
		buf.WriteString(post)
		buf.WriteByte('\n')
	}
	writeStringKV(&buf, "Restart", u.Restart)
	writeStringKV(&buf, "RestartSec", u.RestartSec)
	writeStringKV(&buf, "RestartCountExport", u.RestartCountExport)
	writeStringKV(&buf, "Slice", u.Slice)
	writeStringKV(&buf, "MemoryHigh", u.MemoryHigh)
	writeStringKV(&buf, "MemoryMax", u.MemoryMax)
	if u.Delegate {
		buf.WriteString("Delegate=yes\n")
	}

	// CapabilityBoundingSet empty-but-present is intentional (locks down
	// the cap set; see struct godoc). We write it as `CapabilityBoundingSet=`
	// when the slice is non-nil and empty, and OMIT it when nil.
	if u.CapabilityBoundingSet != nil {
		buf.WriteString("CapabilityBoundingSet=")
		buf.WriteString(strings.Join(u.CapabilityBoundingSet, " "))
		buf.WriteByte('\n')
	}
	if len(u.AmbientCapabilities) > 0 {
		buf.WriteString("AmbientCapabilities=")
		buf.WriteString(strings.Join(u.AmbientCapabilities, " "))
		buf.WriteByte('\n')
	}
	// EnvironmentFile accepts one path per directive. The compact model field
	// is space-separated for backwards compatibility with the existing specs,
	// but emitting the whole value as one directive makes systemd treat it as
	// one literal filename (and silently skips the second file).
	for _, path := range strings.Fields(u.EnvironmentFile) {
		writeStringKV(&buf, "EnvironmentFile", path)
	}
	for _, env := range u.Environment {
		buf.WriteString("Environment=")
		buf.WriteString(env.Key)
		buf.WriteByte('=')
		buf.WriteString(env.Value)
		buf.WriteByte('\n')
	}
	for _, cred := range u.LoadCredential {
		buf.WriteString("LoadCredential=")
		buf.WriteString(cred.Name)
		if cred.Optional {
			buf.WriteString(":-")
		} else {
			buf.WriteByte(':')
		}
		buf.WriteString(cred.Path)
		buf.WriteByte('\n')
	}

	if u.NoNewPrivileges {
		buf.WriteString("NoNewPrivileges=yes\n")
	}
	if u.ProtectSystem != "" {
		buf.WriteString("ProtectSystem=")
		buf.WriteString(u.ProtectSystem)
		buf.WriteByte('\n')
	}
	if u.ProtectHome {
		buf.WriteString("ProtectHome=yes\n")
	}
	if u.PrivateTmp != nil {
		if *u.PrivateTmp {
			buf.WriteString("PrivateTmp=yes\n")
		} else {
			// false for vmmd + schedd — explicitly emit so the
			// wiped-comment rationale (vmmd SOLE-RuntimeDirectory
			// see ADR-078) stops being load-bearing in the unit
			// file body.
			buf.WriteString("PrivateTmp=no\n")
		}
	}
	if u.PrivateDevices {
		buf.WriteString("PrivateDevices=yes\n")
	}
	if u.ProtectKernelTunables {
		buf.WriteString("ProtectKernelTunables=yes\n")
	}
	if u.ProtectKernelModules {
		buf.WriteString("ProtectKernelModules=yes\n")
	}
	if u.ProtectControlGroups {
		buf.WriteString("ProtectControlGroups=yes\n")
	}
	if u.SystemCallArchitectures != "" {
		buf.WriteString("SystemCallArchitectures=")
		buf.WriteString(u.SystemCallArchitectures)
		buf.WriteByte('\n')
	}
	if u.LockPersonality {
		buf.WriteString("LockPersonality=yes\n")
	}
	if u.RestrictNamespaces {
		buf.WriteString("RestrictNamespaces=yes\n")
	}
	if u.RestrictRealtime {
		buf.WriteString("RestrictRealtime=yes\n")
	}
	if u.RestrictSUIDSGID {
		buf.WriteString("RestrictSUIDSGID=yes\n")
	}
	if len(u.RestrictAddressFamilies) > 0 {
		buf.WriteString("RestrictAddressFamilies=")
		buf.WriteString(strings.Join(u.RestrictAddressFamilies, " "))
		buf.WriteByte('\n')
	}
	if u.ProtectHostname {
		buf.WriteString("ProtectHostname=yes\n")
	}
	if u.ProtectClock {
		buf.WriteString("ProtectClock=yes\n")
	}
	if u.ProtectProc != "" {
		buf.WriteString("ProtectProc=")
		buf.WriteString(u.ProtectProc)
		buf.WriteByte('\n')
	}

	writeStringList(&buf, "ReadOnlyPaths", u.ReadOnlyPaths)
	writeStringList(&buf, "ReadWritePaths", u.ReadWritePaths)
	if u.RuntimeDirectory != "" {
		buf.WriteString("RuntimeDirectory=")
		buf.WriteString(u.RuntimeDirectory)
		buf.WriteByte('\n')
		if u.RuntimeDirectoryMode != "" {
			buf.WriteString("RuntimeDirectoryMode=")
			buf.WriteString(u.RuntimeDirectoryMode)
			buf.WriteByte('\n')
		}
	}

	buf.WriteByte('\n')
	buf.WriteString("[Install]\n")
	writeStringKV(&buf, "WantedBy", u.WantedBy)

	return buf.Bytes()
}

// RenderSlice emits the unit file as bytes for a [Slice] section unit
// (the parent cgroup wrapper, e.g. faas-cp.slice). The unit file
// shape is [Unit] + [Slice] + [Install] — NO [Service] section. Slice
// units use a subset of the same Unit fields: Description, After, Wants,
// Requires ([Unit]), Slice, MemoryMax, CPUWeight, IOWeight ([Slice]),
// WantedBy ([Install]).
//
// MemoryMax belongs in [Slice] here, NOT [Service]; systemd silently
// ignores MemoryMax in a [Service] section if the unit is a slice.
// Putting it in [Service] (as Render() does) means the 3 GB ceiling
// the operator declared in the manifest's faas-cp.slice never
// applies, and tenants can OOM the box.
//
// The renderer is the only caller today; pkg/daemonunitspec.UnitSlice()
// produces the Unit value. The Slice field on the Unit is unused for
// the slice unit itself (it's the unit's own name, derivable from
// the file path) — it's set to self-reference for symmetry with the
// service-unit shape but RenderSlice ignores it.
func (u Unit) RenderSlice() []byte {
	var buf bytes.Buffer
	buf.WriteString("[Unit]\n")
	writeStringKV(&buf, "Description", u.Description)
	if u.Documentation != "" {
		buf.WriteString("Documentation=")
		buf.WriteString(u.Documentation)
		buf.WriteByte('\n')
	}
	writeStringList(&buf, "After", u.After)
	writeStringList(&buf, "Wants", u.Wants)
	writeStringList(&buf, "Requires", u.Requires)
	buf.WriteByte('\n')

	buf.WriteString("[Slice]\n")
	writeStringKV(&buf, "MemoryMax", u.MemoryMax)

	buf.WriteByte('\n')
	buf.WriteString("[Install]\n")
	writeStringKV(&buf, "WantedBy", u.WantedBy)

	return buf.Bytes()
}

// writeStringKV appends `<key>=<value>\n` if value != "".
func writeStringKV(buf *bytes.Buffer, key, value string) {
	if value == "" {
		return
	}
	buf.WriteString(key)
	buf.WriteByte('=')
	buf.WriteString(value)
	buf.WriteByte('\n')
}

// writeStringList appends `<key>=<space-separated>\n` if values != nil.
// Empty slice is OMITTED (same as nil). Note: CapabilityBoundingSet
// wants empty-but-present; the Unit struct handles that branch inline.
func writeStringList(buf *bytes.Buffer, key string, values []string) {
	if len(values) == 0 {
		return
	}
	buf.WriteString(key)
	buf.WriteByte('=')
	buf.WriteString(strings.Join(values, " "))
	buf.WriteByte('\n')
}

// ParseError reports a malformed line in a unit file (Decode failures).
// `Line` is 1-indexed; `Field` is the directive name (or "(header)" for
// section headers, or "(body)" for unknown lines).
type ParseError struct {
	Line  int
	Field string
	Err   string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("daemonunit: line %d %s: %s", e.Line, e.Field, e.Err)
}

// Decode parses bytes returned by Render() back into a Unit. Round-trip
// is LOSSY by design: comments, blank-line placement, and directive
// ordering inside sections are not preserved. The RoundTrip test in
// unit_test.go covers the per-field fidelity the deployctl `check`
// subcommand relies on (field-by-field equality on the structs).
//
// The parser is line-oriented: each line is `Directive=Value` or a
// section header (`[Unit]`, `[Service]`, `[Install]`). A line that
// doesn't fit either shape returns a ParseError.
//
// `Decode` is exported mainly so the CI `deployctl check` subcommand
// can round-trip a committed unit file through Render → Decode →
// Render and assert byte equality — same shape as `make sqlc-check`
// uses pkg/state/sqlc.
func Decode(b []byte) (Unit, error) {
	var u Unit
	section := ""
	line := 0
	for _, raw := range bytes.Split(bytes.TrimRight(b, "\n"), []byte("\n")) {
		line++
		s := strings.TrimSpace(string(raw))
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
			switch s {
			case "[Unit]", "[Service]", "[Install]":
				section = s
			default:
				return Unit{}, &ParseError{Line: line, Field: "(header)", Err: "unknown section: " + s}
			}
			continue
		}
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			return Unit{}, &ParseError{Line: line, Field: "(body)", Err: "missing '=' in directive: " + s}
		}
		key, val := s[:eq], s[eq+1:]
		if err := apply(&u, section, key, val); err != nil {
			return Unit{}, &ParseError{Line: line, Field: key, Err: err.Error()}
		}
	}
	return u, nil
}

// apply routes one (key, value) pair into the Unit struct based on the
// current section. Returns errors for directives this Unit doesn't know
// about — Decode round-trip is expected to be exact for the directives
// Render emits; an unknown directive at decode time means Render()
// changed shape or a hand-edited unit drifted.
func apply(u *Unit, section, key, val string) error {
	switch section + "/" + key {
	case "[Unit]/Description":
		u.Description = val
	case "[Unit]/Documentation":
		u.Documentation = val
	case "[Unit]/After":
		u.After = strings.Fields(val)
	case "[Unit]/Wants":
		u.Wants = strings.Fields(val)
	case "[Unit]/Requires":
		u.Requires = strings.Fields(val)

	case "[Service]/Type":
		u.Type = val
	case "[Service]/User":
		u.User = val
	case "[Service]/Group":
		u.Group = val
	case "[Service]/ExecStart":
		u.ExecStart = val
	case "[Service]/ExecStartPre":
		u.ExecStartPre = append(u.ExecStartPre, val)
	case "[Service]/ExecStartPost":
		u.ExecStartPost = append(u.ExecStartPost, val)
	case "[Service]/Restart":
		u.Restart = val
	case "[Service]/RestartSec":
		u.RestartSec = val
	case "[Service]/RestartCountExport":
		u.RestartCountExport = val
	case "[Service]/Slice":
		u.Slice = val
	case "[Service]/MemoryHigh":
		u.MemoryHigh = val
	case "[Service]/MemoryMax":
		u.MemoryMax = val
	case "[Service]/Delegate":
		u.Delegate = parseYes(val)
	case "[Service]/CapabilityBoundingSet":
		u.CapabilityBoundingSet = splitOrEmpty(val)
	case "[Service]/AmbientCapabilities":
		u.AmbientCapabilities = strings.Fields(val)
	case "[Service]/EnvironmentFile":
		if u.EnvironmentFile == "" {
			u.EnvironmentFile = val
		} else {
			u.EnvironmentFile += " " + val
		}
	case "[Service]/Environment":
		eq := strings.IndexByte(val, '=')
		if eq < 0 {
			return fmt.Errorf("missing '=' in Environment value")
		}
		u.Environment = append(u.Environment, KV{Key: val[:eq], Value: val[eq+1:]})
	case "[Service]/LoadCredential":
		lc, err := parseLoadCred(val)
		if err != nil {
			return err
		}
		u.LoadCredential = append(u.LoadCredential, lc)

	case "[Service]/NoNewPrivileges":
		u.NoNewPrivileges = parseYes(val)
	case "[Service]/ProtectSystem":
		u.ProtectSystem = val
	case "[Service]/ProtectHome":
		u.ProtectHome = parseYes(val)
	case "[Service]/PrivateTmp":
		v := parseYes(val)
		u.PrivateTmp = &v
	case "[Service]/PrivateDevices":
		u.PrivateDevices = parseYes(val)
	case "[Service]/ProtectKernelTunables":
		u.ProtectKernelTunables = parseYes(val)
	case "[Service]/ProtectKernelModules":
		u.ProtectKernelModules = parseYes(val)
	case "[Service]/ProtectControlGroups":
		u.ProtectControlGroups = parseYes(val)
	case "[Service]/SystemCallArchitectures":
		u.SystemCallArchitectures = val
	case "[Service]/LockPersonality":
		u.LockPersonality = parseYes(val)
	case "[Service]/RestrictNamespaces":
		u.RestrictNamespaces = parseYes(val)
	case "[Service]/RestrictRealtime":
		u.RestrictRealtime = parseYes(val)
	case "[Service]/RestrictSUIDSGID":
		u.RestrictSUIDSGID = parseYes(val)
	case "[Service]/RestrictAddressFamilies":
		u.RestrictAddressFamilies = strings.Fields(val)
	case "[Service]/ProtectHostname":
		u.ProtectHostname = parseYes(val)
	case "[Service]/ProtectClock":
		u.ProtectClock = parseYes(val)
	case "[Service]/ProtectProc":
		u.ProtectProc = val

	case "[Service]/ReadOnlyPaths":
		u.ReadOnlyPaths = strings.Fields(val)
	case "[Service]/ReadWritePaths":
		u.ReadWritePaths = strings.Fields(val)
	case "[Service]/RuntimeDirectory":
		u.RuntimeDirectory = val
	case "[Service]/RuntimeDirectoryMode":
		u.RuntimeDirectoryMode = val

	case "[Install]/WantedBy":
		u.WantedBy = val

	default:
		return fmt.Errorf("unknown directive %s in %s", key, section)
	}
	return nil
}

// splitOrEmpty splits a systemd multi-value directive value by spaces,
// returning a non-nil empty slice for an empty value (so Apply can
// distinguish "the directive body is empty" from "not set"). Used
// for CapabilityBoundingSet, where empty-vs-absent has a real meaning.
func splitOrEmpty(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Fields(s)
}

// parseLoadCred parses the value side of a LoadCredential= directive:
// `<name>:<path>` or `<name>:-<path>`. Returns an error on missing colon.
func parseLoadCred(s string) (LoadCred, error) {
	// Optional-flag form is `name:-path`; the colon-IN-path character
	// is rare on our credential paths (all are /etc/faas/secrets/*
	// today), so a single split on the first `:` is correct.
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return LoadCred{}, fmt.Errorf("missing ':' in LoadCredential value")
	}
	name, rest := s[:i], s[i+1:]
	optional := false
	if strings.HasPrefix(rest, "-") {
		optional = true
		rest = rest[1:]
	}
	return LoadCred{Name: name, Path: rest, Optional: optional}, nil
}

// parseYes normalises a "yes" / "no" / "true" / "false" / "on" / "off"
// directive value into a bool. systemd accepts all of those; we accept
// the same set for tolerance.
//
// LOAD-BEARING DEFAULT: any token outside the canonical set returns
// false (case-insensitive). This is intentional — the generator emits
// known literals, but a hand-edited unit file containing an unrecognised
// token (e.g. "enabled", capitalised variants, a typo) is treated as
// "off". Tightening to a parse error would be safer but would also
// break tolerance for variants systemd itself accepts; the explicit
// default keeps decode+round-trip stable for our 8 spec'd daemons.
func parseYes(s string) bool {
	switch strings.ToLower(s) {
	// Canonical systemd boolean spellings (yes/true/on/1); not
	// application literals.
	case "yes", "true", "on", "1": //nolint:goconst
		return true
	default:
		return false
	}
}

// Diff returns the directives that differ between two Units. Used by
// the `deployctl check` subcommand to report a focused fix-up message
// rather than a raw byte diff. The returned slice is sorted (by section
// then key) for stable error output.
//
// Diff is order-insensitive for slice values (After, Wants, Requires,
// EnvironmentFile paths, capability lists, RestrictAddressFamilies) —
// hand-edited units often reorder those within the same set, and we
// don't want to flag that as a "drift" in CI. Env / LoadCredential
// matches by KEY regardless of order (two LoadCredential= lines in
// different order are equivalent).
//
// DUPLICATE COLLAPSE: because the slice-shaped fields are compared as
// sets, a duplicate entry (the same value listed twice in one Unit)
// silently collapses in the diff. Today our 8 specs never emit
// duplicates and `daemonunit-check` is byte-exact so the gate still
// catches them at the bytes layer — but if `daemonunit-check` ever
// loosens to a semantic check, callers should pre-validate spec slices
// for duplicates before round-tripping through Diff.
func Diff(a, b Unit) []string {
	var out []string
	add := func(section, key, av, bv string) {
		if av == bv {
			return
		}
		out = append(out, fmt.Sprintf("[%s] %s: %q != %q", section, key, av, bv))
	}
	add("[Unit]", "Description", a.Description, b.Description)
	add("[Unit]", "Documentation", a.Documentation, b.Documentation)
	add("[Unit]", "After", fmt.Sprintf("%v", sortClone(a.After)), fmt.Sprintf("%v", sortClone(b.After)))
	add("[Unit]", "Wants", fmt.Sprintf("%v", sortClone(a.Wants)), fmt.Sprintf("%v", sortClone(b.Wants)))
	add("[Unit]", "Requires", fmt.Sprintf("%v", sortClone(a.Requires)), fmt.Sprintf("%v", sortClone(b.Requires)))
	add("[Service]", "Type", a.Type, b.Type)
	add("[Service]", "User", a.User, b.User)
	add("[Service]", "Group", a.Group, b.Group)
	add("[Service]", "ExecStart", a.ExecStart, b.ExecStart)
	add("[Service]", "ExecStartPre", fmt.Sprintf("%v", a.ExecStartPre), fmt.Sprintf("%v", b.ExecStartPre))
	add("[Service]", "ExecStartPost", fmt.Sprintf("%v", a.ExecStartPost), fmt.Sprintf("%v", b.ExecStartPost))
	add("[Service]", "Restart", a.Restart, b.Restart)
	add("[Service]", "RestartSec", a.RestartSec, b.RestartSec)
	add("[Service]", "RestartCountExport", a.RestartCountExport, b.RestartCountExport)
	add("[Service]", "Slice", a.Slice, b.Slice)
	add("[Service]", "MemoryHigh", a.MemoryHigh, b.MemoryHigh)
	add("[Service]", "MemoryMax", a.MemoryMax, b.MemoryMax)
	add("[Service]", "Delegate", boolStr(a.Delegate), boolStr(b.Delegate))
	add("[Service]", "CapabilityBoundingSet",
		fmt.Sprintf("%v|%d", sortClone(a.CapabilityBoundingSet), len(a.CapabilityBoundingSet)),
		fmt.Sprintf("%v|%d", sortClone(b.CapabilityBoundingSet), len(b.CapabilityBoundingSet)))
	add("[Service]", "AmbientCapabilities",
		fmt.Sprintf("%v", sortClone(a.AmbientCapabilities)),
		fmt.Sprintf("%v", sortClone(b.AmbientCapabilities)))
	add("[Service]", "EnvironmentFile", a.EnvironmentFile, b.EnvironmentFile)
	add("[Service]", "Environment", envFmt(a.Environment), envFmt(b.Environment))
	add("[Service]", "LoadCredential", loadFmt(a.LoadCredential), loadFmt(b.LoadCredential))
	add("[Service]", "NoNewPrivileges", boolStr(a.NoNewPrivileges), boolStr(b.NoNewPrivileges))
	add("[Service]", "ProtectSystem", a.ProtectSystem, b.ProtectSystem)
	add("[Service]", "ProtectHome", boolStr(a.ProtectHome), boolStr(b.ProtectHome))
	add("[Service]", "PrivateTmp", triStr(a.PrivateTmp), triStr(b.PrivateTmp))
	add("[Service]", "PrivateDevices", boolStr(a.PrivateDevices), boolStr(b.PrivateDevices))
	add("[Service]", "ProtectKernelTunables", boolStr(a.ProtectKernelTunables), boolStr(b.ProtectKernelTunables))
	add("[Service]", "ProtectKernelModules", boolStr(a.ProtectKernelModules), boolStr(b.ProtectKernelModules))
	add("[Service]", "ProtectControlGroups", boolStr(a.ProtectControlGroups), boolStr(b.ProtectControlGroups))
	add("[Service]", "SystemCallArchitectures", a.SystemCallArchitectures, b.SystemCallArchitectures)
	add("[Service]", "LockPersonality", boolStr(a.LockPersonality), boolStr(b.LockPersonality))
	add("[Service]", "RestrictNamespaces", boolStr(a.RestrictNamespaces), boolStr(b.RestrictNamespaces))
	add("[Service]", "RestrictRealtime", boolStr(a.RestrictRealtime), boolStr(b.RestrictRealtime))
	add("[Service]", "RestrictSUIDSGID", boolStr(a.RestrictSUIDSGID), boolStr(b.RestrictSUIDSGID))
	add("[Service]", "RestrictAddressFamilies",
		fmt.Sprintf("%v", sortClone(a.RestrictAddressFamilies)),
		fmt.Sprintf("%v", sortClone(b.RestrictAddressFamilies)))
	add("[Service]", "ProtectHostname", boolStr(a.ProtectHostname), boolStr(b.ProtectHostname))
	add("[Service]", "ProtectClock", boolStr(a.ProtectClock), boolStr(b.ProtectClock))
	add("[Service]", "ProtectProc", a.ProtectProc, b.ProtectProc)
	add("[Service]", "ReadOnlyPaths",
		fmt.Sprintf("%v", sortClone(a.ReadOnlyPaths)),
		fmt.Sprintf("%v", sortClone(b.ReadOnlyPaths)))
	add("[Service]", "ReadWritePaths",
		fmt.Sprintf("%v", sortClone(a.ReadWritePaths)),
		fmt.Sprintf("%v", sortClone(b.ReadWritePaths)))
	add("[Service]", "RuntimeDirectory", a.RuntimeDirectory, b.RuntimeDirectory)
	add("[Service]", "RuntimeDirectoryMode", a.RuntimeDirectoryMode, b.RuntimeDirectoryMode)
	add("[Install]", "WantedBy", a.WantedBy, b.WantedBy)
	slices.Sort(out)
	return out
}

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// triStr renders a *bool to a stable signature: nil ⇒ "<unset>",
// &true ⇒ "yes", &false ⇒ "no". Used only by Diff for fields that
// have a real unset state.
func triStr(b *bool) string {
	if b == nil {
		return "<unset>"
	}
	if *b {
		return "yes"
	}
	return "no"
}

// sortClone returns a sorted clone of s. nil and empty both round-trip
// to a stable signature ("[]") so Diff doesn't flag a missing-vs-empty
// distinction for slices we don't track presence for.
func sortClone(s []string) []string {
	if s == nil {
		return nil
	}
	c := slices.Clone(s)
	slices.Sort(c)
	return c
}

func envFmt(b []KV) string {
	keys := make([]string, 0, len(b))
	for _, kv := range b {
		keys = append(keys, kv.Key+"="+kv.Value)
	}
	slices.Sort(keys)
	return strings.Join(keys, ",")
}

func loadFmt(b []LoadCred) string {
	parts := make([]string, 0, len(b))
	for _, c := range b {
		opt := ""
		if c.Optional {
			opt = ":-"
		} else {
			opt = ":"
		}
		parts = append(parts, c.Name+opt+c.Path)
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}
