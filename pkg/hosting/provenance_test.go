package hosting

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestHostingProvenanceValidation(t *testing.T) {
	valid := []*api.HostingReleaseProvenance{nil, {CommitSHA: strings.Repeat("a", 40)}, {CommitSHA: strings.Repeat("b", 64), Repository: "org/repository", Branch: "feature/example", Description: "Ship café"}, {Description: strings.Repeat("x", api.HostingProvenanceDescriptionMaxBytes)}}
	for _, p := range valid {
		if err := ValidateProvenance(p); err != nil {
			t.Fatal(p, err)
		}
	}
	invalid := []api.HostingReleaseProvenance{{}, {CommitSHA: "abc123"}, {CommitSHA: strings.Repeat("A", 40)}, {Repository: " trailing "}, {Repository: "a\nb"}, {Description: "a\u202eb"}, {Description: strings.Repeat("é", api.HostingProvenanceDescriptionMaxBytes)}, {Branch: strings.Repeat("a", api.HostingProvenanceBranchMaxBytes+1)}, {Description: string([]byte{255})}}
	for _, p := range invalid {
		if ValidateProvenance(&p) == nil {
			t.Fatal("invalid provenance accepted", p)
		}
	}
}
