package wire

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
)

// TLSForPeerCN returns a copy of base that, on top of base's own checks,
// accepts only a peer whose leaf CommonName is cn.
//
// The node verifier (VerifyCNClosure) accepts any registered compute node's
// CN, and every compute node holds a leaf for its own name. A client that
// dials one node by its compute_nodes.target_url must also require that
// node's name, or a node able to rewrite target_url could answer for
// another with its own valid leaf (ADR-056: the leaf CN binds to
// compute_nodes.name). A nil base, or an empty cn, returns base unchanged:
// plaintext unix sockets and callers with no name keep their behaviour.
func TLSForPeerCN(base *tls.Config, cn string) *tls.Config {
	if base == nil || cn == "" {
		return base
	}
	cfg := base.Clone()
	prior := cfg.VerifyPeerCertificate
	cfg.VerifyPeerCertificate = func(rawCerts [][]byte, chains [][]*x509.Certificate) error {
		if prior != nil {
			if err := prior(rawCerts, chains); err != nil {
				return err
			}
		}
		return requirePeerCN(rawCerts, chains, cn)
	}
	return cfg
}

func requirePeerCN(rawCerts [][]byte, chains [][]*x509.Certificate, cn string) error {
	var leaf *x509.Certificate
	if len(chains) > 0 && len(chains[0]) > 0 {
		leaf = chains[0][0]
	} else if len(rawCerts) > 0 {
		parsed, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return fmt.Errorf("wire: parse peer leaf: %w", err)
		}
		leaf = parsed
	}
	if leaf == nil {
		return errors.New("wire: peer presented no certificate")
	}
	if leaf.Subject.CommonName != cn {
		return fmt.Errorf("wire: peer certificate is for %q, not the dialled node %q", leaf.Subject.CommonName, cn)
	}
	return nil
}
