package main

import (
	"crypto/tls"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/wire"
)

// traceSpansWriterTarget resolves apid's SpansWriter endpoint for guest
// trace exports (ADR-934). Single-box hosts use the local Unix socket.
// Split-box compute nodes dial apid's private mTLS listener — the one
// gatewayd-internal already uses for spans — with vmmd's node-identity
// apid-client leaf, which apid's node verifier binds to compute_nodes.name.
// A remote target without client TLS is refused rather than sent in
// plaintext.
func traceSpansWriterTarget(getenv func(string) string) (string, *tls.Config, error) {
	if target := getenv("FAAS_APID_OTEL_SPANS_WRITER_SOCKET"); target != "" {
		return target, nil, nil
	}
	target := getenv("FAAS_VMMD_SPANS_WRITER_TARGET")
	if target == "" {
		return "/run/faas/otel_spans_writer.sock", nil, nil
	}
	tlsCfg, err := wire.LoadClientTLSConfigWithPrefix("vmmd_apid_client_",
		getenv("FAAS_VMMD_APID_CLIENT_TLS_CERT_PATH"),
		getenv("FAAS_VMMD_APID_CLIENT_TLS_KEY_PATH"),
		getenv("FAAS_VMMD_APID_CLIENT_TLS_CA_PATH"))
	if err != nil {
		return "", nil, fmt.Errorf("trace receiver apid client TLS: %w", err)
	}
	if tlsCfg == nil && !isLocalSpansTarget(target) {
		return "", nil, fmt.Errorf("trace receiver: remote spans writer target %q requires vmmd apid client mTLS", target)
	}
	return target, tlsCfg, nil
}

func isLocalSpansTarget(target string) bool {
	return strings.HasPrefix(target, "/") || strings.HasPrefix(target, "unix:")
}
