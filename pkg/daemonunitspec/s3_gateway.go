package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitS3Gateway is the canonical unit for the optional branded object-storage
// data plane. It is intentionally kept out of Registry: unlike the core
// control-plane daemons, s3-gatewayd cannot start until an operator has
// installed a qualified provider registry. OptionalRegistry still lets the
// unit generator keep the Ansible artifact in lockstep with this definition.
//
// Provider secrets are supplied through a dedicated EnvironmentFile. The
// provider-neutral registry contains only environment-variable names, never
// secret values. The current and previous host age identities use systemd
// credentials so the non-root daemon can re-seal tenant S3 credentials during
// a host-key rotation without making either private key broadly readable.
func UnitS3Gateway() daemonunit.Unit {
	return daemonunit.Unit{
		Description:           "Gregale S3-compatible object-storage gateway",
		Documentation:         "https://docs.gregale.dev/object-storage",
		After:                 []string{"network-online.target", "postgresql.service", "faas-apid.service", "faas-cp.slice"},
		Wants:                 []string{"faas-cp.slice", "faas-apid.service"},
		Requires:              []string{"postgresql.service"},
		StartLimitIntervalSec: "60s",
		StartLimitBurst:       "5",

		Type:               "notify",
		User:               "faas",
		Group:              "faas",
		ExecStart:          `/opt/faas/current/bin/s3-gatewayd`,
		Restart:            "on-failure",
		RestartSec:         "2s",
		RestartCountExport: "SYSTEMD_RESTARTS_ON_FAILURE",

		Slice:     FaasCPSlice,
		MemoryMax: "512M",

		EnvironmentFile: "-/etc/faas/compute-db.env -/etc/faas/secrets/object-storage/provider.env -/etc/faas/otel.env",
		Environment: []daemonunit.KV{
			{Key: "FAAS_OBJECT_STORAGE_CONFIG", Value: "/etc/faas/object-storage.json"},
			{Key: "FAAS_HOST_AGE_IDENTITY_PATH", Value: "%d/faas_host_age_identity"},
			{Key: "FAAS_HOST_AGE_PREVIOUS_IDENTITY_PATH", Value: "%d/faas_host_age_identity_previous"},
			{Key: "FAAS_S3_GATEWAY_LISTEN_ADDR", Value: "127.0.0.1:8084"},
			{Key: "FAAS_S3_GATEWAY_CONTROL_ADDR", Value: "127.0.0.1:9096"},
			{Key: "FAAS_S3_GATEWAY_SPOOL_DIR", Value: "/var/spool/faas/s3-gatewayd"},
		},
		LoadCredential: []daemonunit.LoadCred{
			{Name: "faas_host_age_identity", Path: "/etc/faas/secrets/host.age"},
			{Name: "faas_host_age_identity_previous", Path: "/etc/faas/secrets/host.age.previous", Optional: true},
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
		RestrictAddressFamilies: []string{"AF_UNIX", "AF_INET"},
		ProtectHostname:         true,
		ProtectClock:            true,
		ProtectProc:             "invisible",

		ReadOnlyPaths:  []string{"/etc/faas"},
		ReadWritePaths: []string{"/var/spool/faas/s3-gatewayd"},

		WantedBy: "multi-user.target",
	}
}
