package api

import "fmt"

// ComposeResources is a Compose service's deploy.replicas and
// deploy.resources.limits (ADR-20261023120000), converted to app settings.
// A nil Replicas or a zero size is undeclared and leaves the app's own
// setting alone.
type ComposeResources struct {
	// Replicas caps a request-mode app's instances (max_concurrency) and
	// fixes a worker's replica count.
	Replicas *int `json:"replicas,omitempty"`
	// MemoryMB is the app's RAM, rounded up to whole MiB.
	MemoryMB int `json:"memory_mb,omitempty"`
	// CPUMillicores is one of the app CPU shares (250, 500, 1000).
	CPUMillicores int `json:"cpu_millicores,omitempty"`
}

// Clone returns a deep copy.
func (r *ComposeResources) Clone() *ComposeResources {
	if r == nil {
		return nil
	}
	out := *r
	if r.Replicas != nil {
		replicas := *r.Replicas
		out.Replicas = &replicas
	}
	return &out
}

// IsZero reports a declaration that sets nothing.
func (r *ComposeResources) IsZero() bool {
	return r == nil || (r.Replicas == nil && r.MemoryMB == 0 && r.CPUMillicores == 0)
}

// Validate checks the shape; plan caps are applied at project admission,
// where the execution mode is known.
func (r *ComposeResources) Validate() error {
	if r == nil {
		return nil
	}
	if r.Replicas != nil && *r.Replicas < 1 {
		return fmt.Errorf("deploy.replicas %d must be at least 1", *r.Replicas)
	}
	if r.MemoryMB < 0 {
		return fmt.Errorf("memory %d MB must be positive", r.MemoryMB)
	}
	if r.CPUMillicores != 0 && !ValidAppCPUMillicores(r.CPUMillicores) {
		return fmt.Errorf("cpu_millicores %d must be one of 250, 500, 1000", r.CPUMillicores)
	}
	return nil
}
