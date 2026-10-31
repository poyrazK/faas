package main

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// dialCredentialReader reaches apid for credential matches alone, over the
// same target and TLS settings as the history reader, when no preview opened
// that connection (ADR 20261029202403 amendment 1). It never stops realtimed:
// without it a keyed Data API server key is refused, and the failure is
// logged.
func dialCredentialReader(ctx context.Context, log *slog.Logger) *apidgrpc.RealtimeHistoryClientImpl {
	tlsCfg, err := wire.LoadClientTLSConfigWithPrefix("realtime_history_",
		os.Getenv("FAAS_REALTIME_HISTORY_TLS_CERT_PATH"),
		os.Getenv("FAAS_REALTIME_HISTORY_TLS_KEY_PATH"),
		os.Getenv("FAAS_REALTIME_HISTORY_TLS_CA_PATH"))
	if err != nil {
		log.Warn("realtimed: apid credential matches unavailable; keyed Data API server keys are refused", "err", err)
		return nil
	}
	nodeName := strings.TrimSpace(os.Getenv("FAAS_NODE_NAME"))
	if nodeName == "" {
		nodeName = state.DefaultLocalNodeName
	}
	reader, err := apidgrpc.DialRealtimeHistory(ctx, getenv("FAAS_REALTIME_HISTORY_TARGET", "/run/faas/request_telemetry.sock"), tlsCfg, nodeName)
	if err != nil {
		log.Warn("realtimed: apid credential matches unavailable; keyed Data API server keys are refused", "err", err)
		return nil
	}
	return reader
}
