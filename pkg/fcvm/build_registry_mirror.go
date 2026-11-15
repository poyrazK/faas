package fcvm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// BuildRegistryMirror returns the handler that serves registry's mirror to
// the builder VM of buildID (ADR-20261110210000 slice 3j-3). cmd/vmmd builds
// it on registryproxy.Mirror, which pulls through secretd by build id, so
// the node never opens the registry password and the guest never sees it.
type BuildRegistryMirror func(buildID, registry string) http.Handler

// SetBuildRegistryMirror makes builder VMs pull their app's private
// registries through mirrors instead of a Docker config with passwords.
// A builder whose request carries no build id (an older builderd) keeps the
// Docker config. NOT safe to call concurrently with Wake; production wires
// it before serving traffic.
func (m *Manager) SetBuildRegistryMirror(mirror BuildRegistryMirror) {
	m.buildRegistryMirror = mirror
}

// leaseBuildMirror is one mirror the builder's guest dials on vsock port.
type leaseBuildMirror struct {
	port    uint32
	handler http.Handler
}

// leaseBuildMirrors is a builder's mirror set, never changed once built.
type leaseBuildMirrors struct {
	mirrors []leaseBuildMirror
}

// list returns the set's mirrors; nil for a lease without any.
func (s *leaseBuildMirrors) list() []leaseBuildMirror {
	if s == nil {
		return nil
	}
	return s.mirrors
}

// buildMirrorPlan is the mirror plan of a builder request, or nil when it
// pulls with a Docker config (no mirror set, no build id, no credentials).
func (m *Manager) buildMirrorPlan(req WakeRequest) []api.BuildRegistryMirror {
	if m.buildRegistryMirror == nil || req.ExportDir == "" || req.BuildID == "" || len(req.BuildRegistryCredentials) == 0 {
		return nil
	}
	hosts := make([]string, 0, len(req.BuildRegistryCredentials))
	for _, cred := range req.BuildRegistryCredentials {
		hosts = append(hosts, cred.Registry)
	}
	return api.PlanBuildRegistryMirrors(hosts)
}

// buildMirrorConfig renders the plan for the guest's secrets.env.
func buildMirrorConfig(plan []api.BuildRegistryMirror) (string, error) {
	raw, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("render build registry mirrors: %w", err)
	}
	return string(raw), nil
}

// leaseBuildMirrors attaches a handler to each planned mirror.
func (m *Manager) leaseBuildMirrors(req WakeRequest) *leaseBuildMirrors {
	plan := m.buildMirrorPlan(req)
	if len(plan) == 0 {
		return nil
	}
	out := make([]leaseBuildMirror, len(plan))
	for i, mirror := range plan {
		out[i] = leaseBuildMirror{port: mirror.VsockPort, handler: m.buildRegistryMirror(req.BuildID, mirror.Registry)}
	}
	return &leaseBuildMirrors{mirrors: out}
}

// buildMirrorReadHeaderTimeout bounds a guest request's header read.
const buildMirrorReadHeaderTimeout = 30 * time.Second

// serveBuildMirrors binds each of a builder's mirror endpoints (Firecracker's
// <uds>_<port>, owned by the jail) before the guest boots and serves it
// until Destroy closes the VM's vsock listeners.
func (v *JailerVMM) serveBuildMirrors(l Lease) error {
	for _, mirror := range l.buildMirrors.list() {
		if err := v.prepareGuestVsockListener(l, mirror.port); err != nil {
			return fmt.Errorf("prepare build registry mirror port %d: %w", mirror.port, err)
		}
		ln := v.guestVsockListener(l.Instance, mirror.port)
		if ln == nil {
			return fmt.Errorf("prepare build registry mirror port %d: prepared listener is missing", mirror.port)
		}
		srv := &http.Server{Handler: mirror.handler, ReadHeaderTimeout: buildMirrorReadHeaderTimeout}
		// Serve returns when Destroy closes the listener; a guest that
		// cannot reach the mirror fails its pull, and the build log says so.
		go func() { _ = srv.Serve(ln) }()
	}
	return nil
}
