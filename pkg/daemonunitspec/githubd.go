package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitGithubd is the canonical unit for faas-githubd — GitHub App
// integration daemon (ADR-046, issue #419).
//
// Wipe-comments-load-bearing rationale:
//
//   - githubd reads the host X25519 private half to authenticate GitHub
//     webhook payloads under the App's signing key. The on-disk file is
//     /etc/faas/secrets/host.age, mode 0400 root:root (spec §11).
//     Like apid, githubd does NOT open the on-disk file directly —
//     systemd copies it into the unit's credential dir (owned by
//     faas:faas) under the %d/faas_host_age_identity path.
//
//     Note: githubd does NOT have a rotation-overlap credential today.
//     apid needs both identities during a 30-day rotate window; githubd
//     reads only the current identity. A future PR may add a similar
//     `:-` LoadCredential for parity; tracked but not blocking.
//
//   - ReadWritePaths=/run/faas is REQUIRED: githubd owns the
//     /run/faas/githubd.sock that the GitHub App webhook handler
//     (apid's /githubd proxy) dials. Without /run/faas in
//     ReadWritePaths, githubd fails to bind the socket with EACCES
//     (PR-E landed this).
//
//   - ReadWritePaths=/var/lib/faas: githubd writes /var/lib/faas
//     attribution hmac key + the github-bot pool credentials.
//
// See ADR-078 for the migration that wiped these from the unit body.
func UnitGithubd() daemonunit.Unit {
	return withRestartPolicy(daemonunit.Unit{
		Description: "onebox-faas githubd — GitHub App integration",
		After:       []string{"network.target", "postgresql.service", "faas-cp.slice"},
		Wants:       []string{"faas-cp.slice"},

		Type:      "simple",
		User:      "faas",
		Group:     "faas",
		ExecStart: `/opt/faas/current/bin/githubd --config /etc/faas/githubd.toml`,

		Slice:     "faas-cp.slice",
		MemoryMax: "256M",

		EnvironmentFile: "/etc/faas/sealed.env",
		Environment: []daemonunit.KV{
			{Key: "FAAS_HOST_AGE_IDENTITY_PATH", Value: "%d/faas_host_age_identity"},
		},
		LoadCredential: []daemonunit.LoadCred{
			{Name: "faas_host_age_identity", Path: "/etc/faas/secrets/host.age"},
		},

		NoNewPrivileges:       true,
		ProtectSystem:         "strict",
		ProtectHome:           true,
		PrivateTmp:            daemonunit.BoolPtr(true),
		ProtectKernelTunables: true,
		ProtectKernelModules:  true,
		ProtectControlGroups:  true,

		ReadOnlyPaths:  []string{"/etc/faas"},
		ReadWritePaths: []string{"/var/log/faas", "/var/lib/faas", "/run/faas"},

		WantedBy: "multi-user.target",
	})
}
