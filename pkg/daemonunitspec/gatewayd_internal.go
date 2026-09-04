package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitGatewaydInternal is the canonical unit for faas-gatewayd-internal
// — routing + wake + proxy (Tier A7 split, ADR-070). It serves the local
// unix socket and may additionally bind a private TCP listener when a
// split-box manifest installs the FAAS_GATEWAY_LISTEN drop-in.
//
// Issue #675 / Tier A7 unified mux: the unix socket now serves BOTH the
// synth routes (schedd → /v1/synthesize, /v1/invocations:dispatch, /healthz)
// AND the customer publicHandler (gatewayd-public → customer traffic).
// The public→internal hop negotiates HTTP/2 cleartext via
// srv.Protocols.SetUnencryptedHTTP2(true) on the SynthServer's
// http.Server (Go 1.24+ stdlib API; the deprecated
// golang.org/x/net/http2/h2c wrapper is gone). The outer Caddy +
// TLS hop terminates H2; the in-box socket hop is plaintext, hence
// H2C rather than H2. See ADR-079 for the unified-mux + H2C
// architecture decision.
//
// Wipe-comments-load-bearing rationale:
//
//   - FAAS_GATEWAY_LISTEN=off is the one-box default. Split-box manifests
//     replace it with a private listener; nftables limits that port to the
//     rendered control-plane CIDRs.
//   - RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6: gRPC to schedd/vmmd
//     is over loopback mTLS (AF_INET) and the unix socket to gatewayd-public
//     (AF_UNIX). AF_INET6 is allowed so the internal dial doesn't fail
//     on v6-only networks (defense in depth; we never connect OUT to v6).
//
// See ADR-078 for the migration that wiped these from the unit body.
//
// Issue #585 / ADR-127 — sealed.env is apid-only; gatewayd-internal
// keeps compute-db.env (DATABASE_URL) + loads per-daemon content via
// EnvironmentFile=/etc/faas/secrets/gatewayd-internal/gatewayd-internal.env
// (0400 root:root). The .env file holds content-shaped env vars:
//   - FAAS_SESSION_KEY=<64 hex chars>
//   - FAAS_TLS_DNS_TOKEN=<provider API token>
//
// These are content-shaped env vars the loader reads via os.Getenv;
// the LoadCredential+%d/<id> pattern only fits PATH-shaped vars
// (env var = tmpfs path; loader does os.ReadFile(env)) — using it
// for content would set the env var to a path string and break the
// loader. The session key content is shared with apid (apid writes/
// validates session cookies that gatewayd-internal forwards); the
// canonical source file /etc/faas/secrets/session.key is read by both
// daemons' loader helpers in their respective config.go, while the
// per-daemon .env file is the systemd-level delivery vehicle.
func UnitGatewaydInternal() daemonunit.Unit {
	return daemonunit.Unit{
		Description:   "onebox-faas gatewayd-internal — routing + wake + proxy (Tier A7 split, ADR-070)",
		Documentation: "https://docs.gregale.dev/ops/gatewayd-internal",
		// ADR-143: gatewayd-internal runs on compute-only nodes where
		// apid + schedd are masked (role_convergence); ordering on them
		// only produced "Unit is masked" noise at every start. It dials
		// both over the split-box mTLS transport, so vmmd is the only
		// local dependency.
		After: []string{"faas-cp.slice", "network-online.target", "faas-vmmd.service"},
		Wants: []string{"faas-cp.slice", "faas-vmmd.service"},

		Type:               "simple",
		User:               "faas",
		Group:              "faas",
		ExecStart:          `/opt/faas/current/bin/gatewayd-internal --config /etc/faas/gatewayd-internal.toml`,
		Restart:            "on-failure",
		RestartSec:         "2s",
		RestartCountExport: "SYSTEMD_RESTARTS_ON_FAILURE",

		Slice:     "faas-cp.slice",
		MemoryMax: "512M",

		// gatewayd-internal reads the shared control-plane database
		// through DATABASE_URL. In a split deployment the root-owned
		// compute env is loaded by systemd; TOML must stay credential-free.
		// Issue #585 / ADR-127: sealed.env dropped; per-daemon
		// gatewayd-internal.env (0400 root:root) holds
		// FAAS_SESSION_KEY + FAAS_TLS_DNS_TOKEN content-shaped vars.
		EnvironmentFile: "-/etc/faas/compute-db.env -/etc/faas/secrets/gatewayd-internal/gatewayd-internal.env",

		Environment: []daemonunit.KV{
			{Key: "FAAS_GATEWAY_LISTEN", Value: "off"},
			// Security review A4 (mirrors faas-apid.service): the session
			// key reaches the daemon as a LoadCredential= path, never as
			// inherited env content. cmd/gatewayd-internal/session_key.go
			// accepts both the PATH-shaped (%d/<id>) and CONTENT-shaped
			// forms; the unit pins the path form so the raw key is only
			// ever readable inside the service's credential tmpfs.
			{Key: "FAAS_SESSION_KEY", Value: "%d/faas_session_key"},
			{Key: "FAAS_LOG_ARCHIVE_CREDS_PATH", Value: "%d/faas_archive_creds"},
		},
		LoadCredential: []daemonunit.LoadCred{
			{Name: "faas_session_key", Path: "/etc/faas/secrets/session.key"},
			{Name: "faas_archive_creds", Path: "/etc/faas/secrets/storage-box/archive-creds.json", Optional: true},
		},

		NoNewPrivileges:         true,
		ProtectSystem:           "strict",
		ProtectHome:             true,
		PrivateTmp:              daemonunit.BoolPtr(true),
		PrivateDevices:          true,
		ProtectKernelTunables:   true,
		ProtectKernelModules:    true,
		ProtectControlGroups:    true,
		SystemCallArchitectures: "native",
		LockPersonality:         true,
		RestrictNamespaces:      true,
		RestrictRealtime:        true,
		RestrictSUIDSGID:        true,
		RestrictAddressFamilies: []string{"AF_UNIX", "AF_INET", "AF_INET6"},
		ProtectHostname:         true,
		ProtectClock:            true,
		ProtectProc:             "invisible",

		ReadOnlyPaths:  []string{"/etc/faas"},
		ReadWritePaths: []string{"/run/faas"},

		WantedBy: "multi-user.target",
	}
}
