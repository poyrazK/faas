package main

import (
	"crypto/tls"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// localVmmdEnv turns on the local data socket for this gateway's own node
// (ADR-20261030230157). Off by default.
const localVmmdEnv = "FAAS_GATEWAY_LOCAL_VMMD_SOCKET"

// localVmmd sends traffic for this gateway's own node to vmmd's local data
// socket instead of over mTLS (ADR-20261030230157). It applies only while that
// vmmd serves the socket, so an N-1 vmmd, or one with the socket off, keeps
// the registry's mTLS target. Every compute_node_changed heartbeat re-resolves
// the target, so the cache follows the socket appearing or going away. A nil
// *localVmmd is the switch turned off.
type localVmmd struct {
	nodeName string
	socket   string
	serving  func(path string) bool
}

// newLocalVmmd returns the local data socket routing for nodeName, or nil when
// the switch is off or the gateway does not know its node.
func newLocalVmmd(enabled bool, nodeName string) *localVmmd {
	nodeName = strings.TrimSpace(nodeName)
	if !enabled || nodeName == "" {
		return nil
	}
	return &localVmmd{nodeName: nodeName, socket: api.VmmdLocalDataSocket, serving: isUnixSocket}
}

// target returns the local data socket's dial target when node is this
// gateway's own node and its vmmd serves the socket.
func (l *localVmmd) target(node state.ComputeNode) (string, bool) {
	if l == nil || node.Name != l.nodeName || !l.serving(l.socket) {
		return "", false
	}
	return "unix://" + l.socket, true
}

// tlsFor returns the TLS config to dial target with: none for the local data
// socket, whose vmmd authenticates the gateway by uid, and vmmdTLS otherwise.
func (l *localVmmd) tlsFor(target string, vmmdTLS *tls.Config) *tls.Config {
	if l != nil && target == "unix://"+l.socket {
		return nil
	}
	return vmmdTLS
}

func isUnixSocket(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}
