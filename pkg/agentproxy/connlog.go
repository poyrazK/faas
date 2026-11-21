package agentproxy

import (
	"context"
	"log/slog"
	"net/netip"
	"time"
)

// ConnLogMessage is the message of every connection log line, the key an
// abuse report's search filters on (docs/runbooks/agent-egress-gateway.md).
const ConnLogMessage = "agent_egress_connection"

// ConnRecord is one agent connection as the connection log records it
// (ADR-20261117185346 §5): who made it, where it went, from which agent
// address, how much it carried and how it ended. A refused connection is
// recorded with whatever the proxy learned before refusing it.
type ConnRecord struct {
	Start     time.Time
	Source    netip.AddrPort // the VM's tunnel address and port
	Node      netip.Addr     // the registering node's tunnel address
	AccountID string
	SandboxID string
	Host      string         // the SNI or Host name
	Dest      netip.AddrPort // the address the VM dialed
	Egress    netip.Addr     // the agent public address it left from
	BytesUp   int64          // VM to destination
	BytesDown int64          // destination to VM
	Outcome   string
}

// logConn writes rec as one JSON line.
func logConn(ctx context.Context, log *slog.Logger, rec *ConnRecord, end time.Time) {
	if log == nil {
		return
	}
	attrs := []slog.Attr{
		slog.String("outcome", rec.Outcome),
		slog.String("source", addrPortString(rec.Source)),
		slog.Time("start", rec.Start.UTC()),
		slog.Int64("duration_ms", end.Sub(rec.Start).Milliseconds()),
	}
	if rec.AccountID != "" {
		attrs = append(attrs, slog.String("node", rec.Node.String()), slog.String("account_id", rec.AccountID),
			slog.String("sandbox_id", rec.SandboxID))
	}
	if rec.Dest.IsValid() {
		attrs = append(attrs, slog.String("dest", rec.Dest.Addr().String()), slog.Int("port", int(rec.Dest.Port())))
	}
	if rec.Host != "" {
		attrs = append(attrs, slog.String("host", rec.Host))
	}
	if rec.Egress.IsValid() {
		attrs = append(attrs, slog.String("egress", rec.Egress.String()),
			slog.Int64("bytes_up", rec.BytesUp), slog.Int64("bytes_down", rec.BytesDown))
	}
	log.LogAttrs(ctx, slog.LevelInfo, ConnLogMessage, attrs...)
}

func addrPortString(a netip.AddrPort) string {
	if !a.IsValid() {
		return ""
	}
	return a.String()
}
