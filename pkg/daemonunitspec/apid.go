// Package daemonunitspec is the per-daemon source of truth for the
// systemd unit files the one-box FaaS platform ships. Each UnitXxx()
// func returns a daemonunit.Unit with the canonical values cd-controlplane
// uses on the EX44 box.
//
// Companion package: pkg/daemonunit — Unit struct + Render / Decode / Diff.
//
// Adding a daemon: add a new spec file here + register it in registry.go
// with a Critical-or-best-effort classification. The generator picks
// the rest up at next `make generate` run.
package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitApid is the canonical unit for faas-apid — public control-plane
// API (spec §4.1).
//
// Wipe-comments-load-bearing rationale that USED to live in the unit
// file body, now preserved here:
//
//   - apid is the SOLE consumer of faas_session_key + faas_host_age_identity
//     LoadCredentials (every other control-plane daemon reads sealed.env
//     but does NOT read these credentials; the 32-byte session key + the
//     host X25519 private half never enter their environments).
//   - The rotation-overlap LoadCredential (`:-` flag) on
//     `faas_host_age_identity_previous` is a no-op pre-rotation but
//     essential during the 30-day window after `gregale host-age rotate
//     --commit` (ADR-057); without it, MFA envelopes sealed under the
//     previous identity 503 CodeCapacity.
//   - FAAS_APID_ADVISORY_SOCK=/run/faas/apid.sock is the stateless-advisory
//     gRPC listener vmmd dials to forward guest-init fanotify batches
//     (Wave 0 PR-C / ADR-047).
//   - FAAS_STATUSPAGE_PATH points cmd/apid at the statuspage HTML under
//     /etc/faas; without this the alert-driven "degraded" pill never
//     renders.
//   - ReadWritePaths includes /var/lib/faas (audit HMAC keys + API key
//     store; PR-M.3 landed this), /var/log/faas, /var/spool/faas.
//
// See ADR-078 for the migration that wiped these from the unit body.
func UnitApid() daemonunit.Unit {
	return withRestartPolicy(daemonunit.Unit{
		Description:   "onebox-faas apid — public control-plane API (spec §4.1)",
		Documentation: "https://docs.gregale.dev/ops/apid",
		After:         []string{"network.target", "faas-cp.slice"},
		Wants:         []string{"faas-cp.slice"},

		Type:      "simple",
		User:      "faas-apid",
		Group:     "faas",
		ExecStart: `/opt/faas/current/bin/apid --config /etc/faas/apid.toml`,

		Slice:     "faas-cp.slice",
		MemoryMax: "256M",

		AmbientCapabilities: []string{"CAP_NET_BIND_SERVICE"},

		EnvironmentFile: "/etc/faas/sealed.env",
		Environment: []daemonunit.KV{
			{Key: "FAAS_SESSION_KEY", Value: "%d/faas_session_key"},
			{Key: "FAAS_HOST_AGE_IDENTITY_PATH", Value: "%d/faas_host_age_identity"},
			{Key: "FAAS_APID_ADVISORY_SOCK", Value: "/run/faas/apid.sock"},
			{Key: "FAAS_STATUSPAGE_PATH", Value: "/etc/faas/statuspage/index.html"},
		},
		LoadCredential: []daemonunit.LoadCred{
			{Name: "faas_session_key", Path: "/etc/faas/secrets/session.key"},
			{Name: "faas_host_age_identity", Path: "/etc/faas/secrets/host.age"},
			{Name: "faas_host_age_identity_previous", Path: "/etc/faas/secrets/host.age.previous", Optional: true},
		},

		NoNewPrivileges:       true,
		ProtectSystem:         "strict",
		ProtectHome:           true,
		PrivateTmp:            daemonunit.BoolPtr(true),
		ProtectKernelTunables: true,
		ProtectKernelModules:  true,
		ProtectControlGroups:  true,

		ReadOnlyPaths:  []string{"/etc/faas"},
		ReadWritePaths: []string{"/var/lib/faas", "/var/log/faas", "/var/spool/faas"},

		WantedBy: "multi-user.target",
	})
}
