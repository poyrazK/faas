package state

import "context"

// PublicBuildURLStore keeps app intent separate from immutable deployment
// inputs. Only apid changes intent; builders read the captured deployment.
type PublicBuildURLStore interface {
	SetAppPublicBuildURLs(context.Context, string, string, map[string]string) error
	DeploymentPublicBuildURLs(context.Context, string) (map[string]string, error)
}
