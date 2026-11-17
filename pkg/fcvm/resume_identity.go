package fcvm

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
)

// ResumeIdentity is the serving instance's placement identity. A process
// restored from a snapshot keeps the FAAS_INSTANCE_ID, FAAS_NODE_ID and
// FAAS_REGION of the instance that captured it, because a process's
// environment is fixed at exec. The resume hook carries the current values so
// guest-init can publish them in the instance identity file
// (ADR-20261107160000).
type ResumeIdentity struct {
	InstanceID string
	NodeID     string
	Region     string
}

// resumeIdentityWire is the resume-hook JSON shape. guest-init ignores an
// absent field, so an older guest and an older vmmd stay compatible.
type resumeIdentityWire struct {
	InstanceID string `json:"instanceId"`
	NodeID     string `json:"nodeId,omitempty"`
	Region     string `json:"region,omitempty"`
}

type resumeIdentityContextKey struct{}

// withResumeIdentity attaches id for every resume hook sent under ctx. An
// identity without an instance ID is not attached.
func withResumeIdentity(ctx context.Context, id ResumeIdentity) context.Context {
	if id.InstanceID == "" {
		return ctx
	}
	return context.WithValue(ctx, resumeIdentityContextKey{}, id)
}

// resumeIdentityWireFromContext returns the payload field, or nil when the
// caller attached no identity.
func resumeIdentityWireFromContext(ctx context.Context) *resumeIdentityWire {
	id, ok := ctx.Value(resumeIdentityContextKey{}).(ResumeIdentity)
	if !ok || id.InstanceID == "" {
		return nil
	}
	return &resumeIdentityWire{InstanceID: id.InstanceID, NodeID: id.NodeID, Region: id.Region}
}

// resumeIdentityFromAPIEnv reads the platform identity schedd appended to the
// wake's api env (pkg/sched appendPlatformIdentity). Malformed or empty input
// yields the zero identity, which no hook carries.
func resumeIdentityFromAPIEnv(apiEnvJSON []byte) ResumeIdentity {
	if len(apiEnvJSON) == 0 {
		return ResumeIdentity{}
	}
	env := map[string]string{}
	if err := json.Unmarshal(apiEnvJSON, &env); err != nil {
		return ResumeIdentity{}
	}
	return ResumeIdentity{
		InstanceID: env[api.PlatformInstanceIDEnv],
		NodeID:     env[api.PlatformNodeIDEnv],
		Region:     env[api.PlatformRegionEnv],
	}
}
