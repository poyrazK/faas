// adr: 20261108124106
package pgproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"testing"

	"google.golang.org/grpc"
)

// Each session dial accepts only the woken node's vmmd leaf (<id>.faas),
// and two nodes behind one target never share a connection.
func TestVmmdDialerBindsTheDialToTheWokenNode(t *testing.T) {
	var dials []*tls.Config
	d := NewVmmdDialerWith(&tls.Config{MinVersion: tls.VersionTLS13}, func(_ context.Context, _ string, cfg *tls.Config) (*grpc.ClientConn, error) {
		dials = append(dials, cfg)
		return nil, errors.New("test: dial refused")
	})
	target := "tcp://192.0.2.1:50051"
	_, _ = d.Dial(context.Background(), Backend{Target: target, Node: "db-1"}, "db")
	_, _ = d.Dial(context.Background(), Backend{Target: target, Node: "db-2.faas"}, "db")
	if len(dials) != 2 {
		t.Fatalf("dials = %d, want one per node", len(dials))
	}
	leaf := func(cn string) [][]*x509.Certificate {
		return [][]*x509.Certificate{{{Subject: pkix.Name{CommonName: cn}}}}
	}
	for i, c := range []struct{ want, other string }{{"db-1.faas", "db-2.faas"}, {"db-2.faas", "db-1.faas"}} {
		cfg := dials[i]
		if cfg == nil || cfg.VerifyPeerCertificate == nil {
			t.Fatalf("dial %d has no node binding", i)
		}
		if err := cfg.VerifyPeerCertificate(nil, leaf(c.want)); err != nil {
			t.Fatalf("dial %d refused %s: %v", i, c.want, err)
		}
		if cfg.VerifyPeerCertificate(nil, leaf(c.other)) == nil {
			t.Fatalf("dial %d for %s accepted %s", i, c.want, c.other)
		}
	}
}
