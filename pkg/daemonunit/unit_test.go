package daemonunit

import (
	"bytes"
	"strings"
	"testing"
)

// TestRender_BasicSectionOrder checks that Render emits
// [Unit], [Service], [Install] in that order — every shipped faas unit
// has them in this order, and the make proto-check / sqlc-check /
// egress-check generators all use predictable section ordering.
func TestRender_BasicSectionOrder(t *testing.T) {
	u := Unit{
		Description: "test",
		Type:        "simple",
		ExecStart:   "/bin/true",
		Restart:     "on-failure",
		WantedBy:    "multi-user.target",
	}
	got := string(u.Render())
	// Suppress description-based parsing — just confirm section ordering.
	if !strings.HasPrefix(got, "[Unit]\n") {
		t.Fatalf("Render didn't start with [Unit]; got %q", got[:min(40, len(got))])
	}
	unitIdx := strings.Index(got, "[Unit]")
	svcIdx := strings.Index(got, "[Service]")
	installIdx := strings.Index(got, "[Install]")
	if !(unitIdx < svcIdx && svcIdx < installIdx) {
		t.Fatalf("section order broken: [Unit]=%d [Service]=%d [Install]=%d\n---\n%s", unitIdx, svcIdx, installIdx, got)
	}
}

// TestRender_LoadCredentialOptionalFlag is the load-bearing pattern for
// apid's rotation overlap (issue #316 / ADR-057). The OutputOptional
// LoadCred renders `name:-path` (the missing-file-tolerant form).
func TestRender_LoadCredentialOptionalFlag(t *testing.T) {
	u := Unit{
		Type:      "simple",
		ExecStart: "/bin/true",
		LoadCredential: []LoadCred{
			{Name: "faas_session_key", Path: "/etc/faas/secrets/session.key"},
			{Name: "faas_host_age_identity_previous", Path: "/etc/faas/secrets/host.age.previous", Optional: true},
		},
	}
	got := string(u.Render())
	if !strings.Contains(got, "LoadCredential=faas_session_key:/etc/faas/secrets/session.key\n") {
		t.Errorf("LoadCredential colon form missing\n%s", got)
	}
	if !strings.Contains(got, "LoadCredential=faas_host_age_identity_previous:-/etc/faas/secrets/host.age.previous\n") {
		t.Errorf("LoadCredential optional-flag (:-) form missing\n%s", got)
	}
}

// TestRender_PercentDSubstitution covers the apid MFA/host-age identity
// path (LoadCredential FAAS_HOST_AGE_IDENTITY_PATH=%d/...). %d is
// systemd's "CREDENTIALS_DIRECTORY" specifier (substituted at ExecStart
// time with the unit's credential dir).
func TestRender_PercentDSubstitution(t *testing.T) {
	u := Unit{
		Type:      "simple",
		ExecStart: "/bin/true",
		Environment: []KV{
			{Key: "FAAS_SESSION_KEY", Value: "%d/faas_session_key"},
			{Key: "FAAS_HOST_AGE_IDENTITY_PATH", Value: "%d/faas_host_age_identity"},
		},
	}
	got := string(u.Render())
	if !strings.Contains(got, "Environment=FAAS_SESSION_KEY=%d/faas_session_key\n") {
		t.Errorf("%%d substitution dropped: %s", got)
	}
	if !strings.Contains(got, "Environment=FAAS_HOST_AGE_IDENTITY_PATH=%d/faas_host_age_identity\n") {
		t.Errorf("%%d substitution dropped: %s", got)
	}
}

// TestRender_PrivateTmpTripleState covers the vmmd/schedd `=no` form.
// nil ⇒ omit; &true ⇒ `=yes`; &false ⇒ `=no`.
func TestRender_PrivateTmpTripleState(t *testing.T) {
	cases := []struct {
		name     string
		in       *bool
		wantLine string // "" ⇒ must not appear
	}{
		{"unset", nil, ""},
		{"true", BoolPtr(true), "PrivateTmp=yes\n"},
		{"false", BoolPtr(false), "PrivateTmp=no\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := Unit{
				Type:       "simple",
				ExecStart:  "/bin/true",
				PrivateTmp: tc.in,
			}
			got := string(u.Render())
			has := strings.Contains(got, tc.wantLine)
			if tc.wantLine == "" {
				if strings.Contains(got, "PrivateTmp=") {
					t.Errorf("unset should not emit PrivateTmp=; got:\n%s", got)
				}
				return
			}
			if !has {
				t.Errorf("expected %q in output, got:\n%s", strings.TrimSpace(tc.wantLine), got)
			}
		})
	}
}

// TestRender_CapabilityBoundingSetSemantics pins down three cases:
// (1) nil            ⇒ omit directive (legacy behaviour for daemons
//
//	that don't care, e.g. vmmd which inherits systemd's default)
//
// (2) [] (empty slice) ⇒ emit `CapabilityBoundingSet=` (empty body —
//
//	systemd locks the unit to no caps)
//
// (3) non-empty       ⇒ emit cap list.
func TestRender_CapabilityBoundingSetSemantics(t *testing.T) {
	t.Run("nil-omits", func(t *testing.T) {
		u := Unit{Type: "simple", ExecStart: "/bin/true"}
		if strings.Contains(string(u.Render()), "CapabilityBoundingSet=") {
			t.Errorf("nil CapabilityBoundingSet should omit; got:\n%s", u.Render())
		}
	})
	t.Run("empty-emits-blank", func(t *testing.T) {
		u := Unit{Type: "simple", ExecStart: "/bin/true", CapabilityBoundingSet: []string{}}
		if !bytes.Contains(u.Render(), []byte("CapabilityBoundingSet=\n")) {
			t.Errorf("empty CapabilityBoundingSet should emit blank directive; got:\n%s", u.Render())
		}
	})
	t.Run("populated-emits-list", func(t *testing.T) {
		u := Unit{Type: "simple", ExecStart: "/bin/true",
			CapabilityBoundingSet: []string{"cap_chown", "cap_dac_override", "cap_kill"}}
		got := u.Render()
		if !bytes.Contains(got, []byte("CapabilityBoundingSet=cap_chown cap_dac_override cap_kill\n")) {
			t.Errorf("populated CapabilityBoundingSet should emit space-separated list; got:\n%s", got)
		}
	})
}

// TestRender_AmbientCapabilitiesOmittedByDefault covers the imaged
// post-DEPLOY-1 state: AmbientCapabilities OMITTED (no ambient caps)
// as the canonical default, while ad-hoc tests still emit when set.
func TestRender_AmbientCapabilitiesOmittedByDefault(t *testing.T) {
	u := Unit{Type: "simple", ExecStart: "/bin/true"}
	if strings.Contains(string(u.Render()), "AmbientCapabilities") {
		t.Errorf("unset AmbientCapabilities should be omitted; got:\n%s", u.Render())
	}
	u2 := Unit{Type: "simple", ExecStart: "/bin/true", AmbientCapabilities: []string{"CAP_NET_BIND_SERVICE"}}
	if !strings.Contains(string(u2.Render()), "AmbientCapabilities=CAP_NET_BIND_SERVICE\n") {
		t.Errorf("populated AmbientCapabilities should emit; got:\n%s", u2.Render())
	}
}

// TestRender_ExecStartPreOrdering checks vmmd's two ExecStartPre=chown
// /chmod lines are emitted BEFORE ExecStart= in directive order
// (systemd rule: pre-commands must come first).
func TestRender_ExecStartPreOrdering(t *testing.T) {
	u := Unit{
		Type:      "simple",
		ExecStart: "/opt/faas/bin/vmmd",
		ExecStartPre: []string{
			"/usr/bin/chown root:faas /run/faas",
			"/usr/bin/chmod 0775 /run/faas",
		},
	}
	got := string(u.Render())
	preIdx := strings.Index(got, "ExecStartPre=")
	execIdx := strings.Index(got, "ExecStart=")
	if preIdx < 0 || execIdx < 0 || preIdx > execIdx {
		t.Fatalf("ExecStartPre must precede ExecStart. got (pre=%d exec=%d):\n%s", preIdx, execIdx, got)
	}
}

func TestRender_ExecStartPostRoundTrip(t *testing.T) {
	u := Unit{
		Type:          "simple",
		ExecStart:     "/opt/faas/bin/vmmd",
		ExecStartPost: []string{"/usr/bin/chown root:faas /run/faas", "/usr/bin/chmod 0775 /run/faas"},
	}
	rendered := string(u.Render())
	if !strings.Contains(rendered, "ExecStart=/opt/faas/bin/vmmd\nExecStartPost=/usr/bin/chown root:faas /run/faas\n") {
		t.Fatalf("ExecStartPost must follow ExecStart; got:\n%s", rendered)
	}
	decoded, err := Decode([]byte(rendered))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got := strings.Join(decoded.ExecStartPost, "|"); got != strings.Join(u.ExecStartPost, "|") {
		t.Fatalf("ExecStartPost round trip = %q, want %q", got, strings.Join(u.ExecStartPost, "|"))
	}
}

// TestRender_RuntimeDirectoryPair pins that vmmd's RuntimeDirectory +
// RuntimeDirectoryMode are emitted as a contiguous pair (systemd does
// not require adjacency, but we emit them together so the unit file
// reads as a logical grouping).
func TestRender_RuntimeDirectoryPair(t *testing.T) {
	u := Unit{
		Type:                 "simple",
		ExecStart:            "/bin/true",
		RuntimeDirectory:     "faas",
		RuntimeDirectoryMode: "0775",
	}
	got := string(u.Render())
	want := "RuntimeDirectory=faas\nRuntimeDirectoryMode=0775\n"
	if !strings.Contains(got, want) {
		t.Errorf("RuntimeDirectory pair missing or out of order; got:\n%s", got)
	}
}

// TestRender_RestrictAddressFamiliesAsList covers the gatewayd-internal pair
// (gatewayd-public uses AF_UNIX AF_INET; gatewayd-internal uses AF_UNIX
// AF_INET AF_INET6 for the loopback control listener).
func TestRender_RestrictAddressFamiliesAsList(t *testing.T) {
	u := Unit{
		Type: "simple", ExecStart: "/bin/true",
		RestrictAddressFamilies: []string{"AF_UNIX", "AF_INET"},
	}
	got := string(u.Render())
	if !strings.Contains(got, "RestrictAddressFamilies=AF_UNIX AF_INET\n") {
		t.Errorf("RestrictAddressFamilies list wrong; got:\n%s", got)
	}
}

// TestDecode_RoundTripBasic asserts Decode parses back what Render emits.
func TestDecode_RoundTripBasic(t *testing.T) {
	u := Unit{
		Description:           "test unit",
		Documentation:         "https://docs.example.com",
		After:                 []string{"network.target"},
		Wants:                 []string{"faas-cp.slice"},
		Type:                  "simple",
		User:                  "faas-apid",
		Group:                 "faas",
		ExecStart:             "/opt/faas/bin/apid",
		ExecStartPre:          []string{"/usr/bin/chmod 0660 /run/faas/apid.sock"},
		Restart:               "on-failure",
		RestartSec:            "2s",
		Slice:                 "faas-cp.slice",
		MemoryHigh:            "192M",
		MemoryMax:             "256M",
		CapabilityBoundingSet: []string{},
		Environment: []KV{
			{Key: "FAAS_HOST_AGE_IDENTITY_PATH", Value: "%d/faas_host_age_identity"},
			{Key: "FAAS_SESSION_KEY", Value: "%d/faas_session_key"},
		},
		LoadCredential: []LoadCred{
			{Name: "faas_session_key", Path: "/etc/faas/secrets/session.key"},
			{Name: "faas_host_age_identity", Path: "/etc/faas/secrets/host.age"},
			{Name: "faas_host_age_identity_previous", Path: "/etc/faas/secrets/host.age.previous", Optional: true},
		},
		NoNewPrivileges:       true,
		ProtectSystem:         "strict",
		ProtectHome:           true,
		PrivateTmp:            BoolPtr(true),
		ProtectKernelTunables: true,
		ProtectKernelModules:  true,
		ProtectControlGroups:  true,
		ReadOnlyPaths:         []string{"/etc/faas"},
		ReadWritePaths:        []string{"/var/lib/faas", "/var/log/faas", "/var/spool/faas"},
		WantedBy:              "multi-user.target",
	}
	rendered := u.Render()
	parsed, err := Decode(rendered)
	if err != nil {
		t.Fatalf("Decode failed: %v\n---\n%s", err, rendered)
	}
	// Field-by-field assertion rather than full-Unit Equals so we can
	// pinpoint mismatches. (Unit has no Eq method by design.)
	if u.Description != parsed.Description {
		t.Errorf("Description: %q != %q", u.Description, parsed.Description)
	}
	if u.User != parsed.User {
		t.Errorf("User: %q != %q", u.User, parsed.User)
	}
	if u.Slice != parsed.Slice {
		t.Errorf("Slice: %q != %q", u.Slice, parsed.Slice)
	}
	if u.MemoryHigh != parsed.MemoryHigh {
		t.Errorf("MemoryHigh: %q != %q", u.MemoryHigh, parsed.MemoryHigh)
	}
	if u.MemoryMax != parsed.MemoryMax {
		t.Errorf("MemoryMax: %q != %q", u.MemoryMax, parsed.MemoryMax)
	}
	if (u.PrivateTmp == nil) != (parsed.PrivateTmp == nil) {
		t.Errorf("PrivateTmp nilness: %v vs %v", u.PrivateTmp, parsed.PrivateTmp)
	}
	if len(u.LoadCredential) != len(parsed.LoadCredential) {
		t.Errorf("LoadCredential len: %d != %d", len(u.LoadCredential), len(parsed.LoadCredential))
	}
	for i := range u.LoadCredential {
		if u.LoadCredential[i] != parsed.LoadCredential[i] {
			t.Errorf("LoadCredential[%d]: %+v != %+v", i, u.LoadCredential[i], parsed.LoadCredential[i])
		}
	}
}

// TestDiff_OrderInsensitive asserts hand-reordered After/Wants/Requires
// (a common foot-gun in the legacy 3-tree setup) does NOT register as
// a diff. The Cap list is matched by SET, not by order.
func TestDiff_OrderInsensitive(t *testing.T) {
	a := Unit{
		After:                   []string{"network.target", "faas-cp.slice"},
		Wants:                   []string{"faas-cp.slice", "faas-apid.service"},
		Type:                    "simple",
		ExecStart:               "/bin/true",
		RestrictAddressFamilies: []string{"AF_UNIX", "AF_INET"},
		CapabilityBoundingSet:   []string{"cap_a", "cap_b"},
	}
	b := Unit{
		After:                   []string{"faas-cp.slice", "network.target"},    // reordered
		Wants:                   []string{"faas-apid.service", "faas-cp.slice"}, // reordered
		Type:                    "simple",
		ExecStart:               "/bin/true",
		RestrictAddressFamilies: []string{"AF_INET", "AF_UNIX"}, // reordered
		CapabilityBoundingSet:   []string{"cap_b", "cap_a"},     // reordered
	}
	got := Diff(a, b)
	if len(got) != 0 {
		t.Errorf("Diff should be empty for reorders; got %v", got)
	}
}

// TestDiff_DetectsRealChanges pins that Diff fires on a real change
// (MemoryMax 256M vs 512M). The test is here to make sure Diff isn't
// silently broken — every `deployctl check` call relies on it firing.
func TestDiff_DetectsRealChanges(t *testing.T) {
	a := Unit{Type: "simple", ExecStart: "/bin/true", MemoryMax: "256M"}
	b := Unit{Type: "simple", ExecStart: "/bin/true", MemoryMax: "512M"}
	got := Diff(a, b)
	if len(got) != 1 || !strings.Contains(got[0], "MemoryMax") {
		t.Errorf("Diff should fire on MemoryMax; got %v", got)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestRender_MemoryHighOmittedWhenEmpty pins the opt-in shape: only vmmd
// sets a soft limit today, so every other unit must render without a
// MemoryHigh line. An always-emitted `MemoryHigh=` would be a silent
// behaviour change on eight daemons.
func TestRender_MemoryHighOmittedWhenEmpty(t *testing.T) {
	got := string(Unit{Type: "simple", ExecStart: "/bin/true", MemoryMax: "256M"}.Render())
	if strings.Contains(got, "MemoryHigh") {
		t.Errorf("MemoryHigh must be omitted when unset; got:\n%s", got)
	}
}

// TestRender_MemoryHighPrecedesMemoryMax pins the directive order. systemd
// does not care, but the generated files are byte-compared by
// `make generate-check`, so the order is part of the contract.
func TestRender_MemoryHighPrecedesMemoryMax(t *testing.T) {
	got := string(Unit{Type: "simple", ExecStart: "/bin/true", MemoryHigh: "512M", MemoryMax: "1G"}.Render())
	hi, max := strings.Index(got, "MemoryHigh="), strings.Index(got, "MemoryMax=")
	if hi < 0 || max < 0 {
		t.Fatalf("both directives must render; got:\n%s", got)
	}
	if hi > max {
		t.Errorf("MemoryHigh must precede MemoryMax; got:\n%s", got)
	}
}

// TestDiff_FiresOnMemoryHigh guards the drift gate: a spec that changes
// only the soft limit must still be reported, otherwise `make
// generate-check` would pass while the committed units disagree.
func TestDiff_FiresOnMemoryHigh(t *testing.T) {
	a := Unit{Type: "simple", ExecStart: "/bin/true", MemoryHigh: "256M"}
	b := Unit{Type: "simple", ExecStart: "/bin/true", MemoryHigh: "512M"}
	got := Diff(a, b)
	if len(got) != 1 || !strings.Contains(got[0], "MemoryHigh") {
		t.Errorf("Diff should fire on MemoryHigh; got %v", got)
	}
}
