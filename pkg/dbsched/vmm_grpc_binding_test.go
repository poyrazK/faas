// adr: 20261108124106
package dbsched_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"testing"

	"google.golang.org/grpc"

	"github.com/onebox-faas/faas/pkg/dbsched"
)

// Each database node's dial accepts only <id>.faas, and two nodes that
// share a target_url never share a connection.
func TestGRPCVMMBindsEachDialToTheNode(t *testing.T) {
	var dials []*tls.Config
	vmm := dbsched.NewGRPCVMMWithDial(&tls.Config{MinVersion: tls.VersionTLS13}, func(_ context.Context, _ string, cfg *tls.Config) (*grpc.ClientConn, error) {
		dials = append(dials, cfg)
		return nil, errors.New("test: dial refused")
	})
	target := "tcp://192.0.2.1:50051"
	_ = vmm.DeleteVolume(context.Background(), dbsched.Node{ID: "db-1", TargetURL: target}, "vol")
	_ = vmm.DeleteVolume(context.Background(), dbsched.Node{ID: "db-2.faas", TargetURL: target}, "vol")
	if len(dials) != 2 {
		t.Fatalf("dials = %d, want one per node", len(dials))
	}
	leaf := func(cn string) [][]*x509.Certificate {
		return [][]*x509.Certificate{{{Subject: pkix.Name{CommonName: cn}}}}
	}
	for i, want := range []string{"db-1.faas", "db-2.faas"} {
		cfg := dials[i]
		if cfg == nil || cfg.VerifyPeerCertificate == nil {
			t.Fatalf("dial %d has no node binding", i)
		}
		if err := cfg.VerifyPeerCertificate(nil, leaf(want)); err != nil {
			t.Fatalf("dial %d refused %s: %v", i, want, err)
		}
		other := "db-2.faas"
		if want == other {
			other = "db-1.faas"
		}
		if cfg.VerifyPeerCertificate(nil, leaf(other)) == nil {
			t.Fatalf("dial %d for %s accepted %s", i, want, other)
		}
	}
}
