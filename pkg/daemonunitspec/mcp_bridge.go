package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitMcpBridge returns the hosted management MCP server
// (ADR-20261026195952): mcp-bridged, serving https://api.<platform domain>/mcp
// behind gatewayd-public on loopback. Its state lives in Postgres under a
// lease; the independent state key arrives as a systemd credential. Agents'
// provisioning commands and uploads run the active release's gregale CLI as
// subprocesses with an empty private home, so the unit needs only a private
// /tmp, and a CLI release does not restart it. The default OAuth callbacks are the published Claude and ChatGPT ones
// plus loopback; an operator replaces ExecStart in a drop-in to change them.
// Metrics are on 127.0.0.1:9110 (the flag's default). The connection admin
// socket sits in a 0700 runtime directory the service user owns: operators
// list and revoke connections as that user with gregale mcp connections.
func UnitMcpBridge() daemonunit.Unit {
	return daemonunit.Unit{
		Description:           "Gregale hosted management MCP server",
		Documentation:         "https://gregale.dev/docs/mcp",
		After:                 []string{"network-online.target", "postgresql.service", "faas-apid.service", "faas-cp.slice"},
		Wants:                 []string{"faas-cp.slice", "faas-apid.service"},
		Requires:              []string{"postgresql.service"},
		StartLimitIntervalSec: "60s",
		StartLimitBurst:       "5",

		Type:  "simple",
		User:  "faas-mcp-bridge",
		Group: "faas",
		ExecStart: `/opt/faas/current/bin/mcp-bridged --state-key-file %d/faas_mcp_bridge_state_key --admin-socket /run/faas-mcp-bridge/admin.sock` +
			` --oauth-redirect-uri https://claude.ai/api/mcp/auth_callback --oauth-redirect-uri https://claude.com/api/mcp/auth_callback` +
			` --oauth-redirect-uri https://chatgpt.com/connector_platform_oauth_redirect` +
			` --oauth-redirect-uri http://localhost/callback --oauth-redirect-uri http://127.0.0.1/callback`,
		Restart:    "on-failure",
		RestartSec: "2s",

		RuntimeDirectory:     "faas-mcp-bridge",
		RuntimeDirectoryMode: "0700",

		Slice:     FaasCPSlice,
		MemoryMax: "1G",
		TasksMax:  "256",

		EnvironmentFile: "-/etc/faas/compute-db.env",
		LoadCredential: []daemonunit.LoadCred{
			{Name: "faas_mcp_bridge_state_key", Path: "/etc/faas/secrets/mcp-bridge/state.key"},
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

		ReadOnlyPaths: []string{"/etc/faas"},

		WantedBy: "multi-user.target",
	}
}
