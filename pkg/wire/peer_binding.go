package wire

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
)

// ErrPeerNotDialedNode is returned by a handshake whose server leaf belongs to
// a different registered node than the one the caller meant to reach
// (ADR-20261108124106).
var ErrPeerNotDialedNode = errors.New("wire: peer is not the dialed node")

// ExpectPeerCN returns a copy of cfg whose handshake also requires the server
// leaf's Subject CN to equal cn, the compute_nodes.name of the node the caller
// is dialing. cfg's own checks (chain trust, SAN, EKU and any registry hook)
// run first.
//
// The registry hook alone accepts any registered node, and the dialed address
// comes from compute_nodes.target_url, a row compute nodes can write: without
// this binding a compromised node could point another node's target_url at
// itself and receive that node's VM-control calls. An empty cn fails every
// handshake rather than falling back to the registry check. A nil cfg (unix
// socket targets) stays nil.
func ExpectPeerCN(cfg *tls.Config, cn string) *tls.Config {
	if cfg == nil {
		return nil
	}
	out := cfg.Clone()
	prev := cfg.VerifyPeerCertificate
	out.VerifyPeerCertificate = func(raw [][]byte, chains [][]*x509.Certificate) error {
		if prev != nil {
			if err := prev(raw, chains); err != nil {
				return err
			}
		}
		if cn == "" {
			return fmt.Errorf("%w: no node name to bind the dial to", ErrPeerNotDialedNode)
		}
		if len(chains) == 0 || len(chains[0]) == 0 {
			return errors.New("wire: node binding invoked without a verified chain")
		}
		if got := chains[0][0].Subject.CommonName; got != cn {
			return fmt.Errorf("%w: dialed %q, peer presented %q", ErrPeerNotDialedNode, cn, got)
		}
		return nil
	}
	return out
}
