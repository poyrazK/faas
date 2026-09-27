// Package runtimepolicyproto defines the small guest/host wire contract for
// live runtime policy updates. It intentionally has no dependencies on the
// VMM or guest-init implementations so both sides can share the framing.
package runtimepolicyproto

const (
	AppCPULimitMessageType  = 4
	AppCPULimitMaxBodyBytes = 1024
)

type AppCPULimitUpdate struct {
	CPUMillicores int `json:"cpu_millicores"`
}
