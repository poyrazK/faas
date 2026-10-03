package imaged

import (
	"context"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const PythonDataBaseRefEnv = "FAAS_EXECUTION_PYTHON_DATA_V1_BASE_REF"

// EnsureExecutionProfileBases stages only explicitly configured profiles.
// Use the existing digest-verified staging path and distinct base keys; unused
// profiles consume no shared-base residency and never replace function bases.
func (h *Handler) EnsureExecutionProfileBases(ctx context.Context, arch string, envLookup func(string) string) ([]EnsureBasesResult, error) {
	if envLookup == nil {
		return nil, fmt.Errorf("imaged: execution profile environment lookup is required")
	}
	ref := strings.TrimSpace(envLookup(PythonDataBaseRefEnv))
	if ref == "" {
		return nil, nil
	}
	if arch != "amd64" {
		return nil, fmt.Errorf("imaged: python-data-v1 image requires amd64")
	}
	return h.EnsureBases(ctx, arch, []RuntimeBaseRef{{Runtime: string(api.ExecutionProfilePythonDataV1), Ref: ref, EnvOverride: PythonDataBaseRefEnv}}, envLookup)
}
