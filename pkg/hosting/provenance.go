package hosting

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

func ValidateProvenance(p *api.HostingReleaseProvenance) error {
	if p == nil {
		return nil
	}
	if *p == (api.HostingReleaseProvenance{}) {
		return errors.New("empty release provenance")
	}
	for _, field := range []struct {
		s   string
		max int
	}{{p.Repository, api.HostingProvenanceRepositoryMaxBytes}, {p.Branch, api.HostingProvenanceBranchMaxBytes}, {p.Description, api.HostingProvenanceDescriptionMaxBytes}} {
		if !utf8.ValidString(field.s) || len(field.s) > field.max || strings.TrimSpace(field.s) != field.s {
			return errors.New("invalid release provenance text")
		}
		for _, r := range field.s {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				return errors.New("invalid release provenance control character")
			}
		}
	}
	if p.CommitSHA != "" {
		if len(p.CommitSHA) != 40 && len(p.CommitSHA) != 64 {
			return errors.New("commit SHA must be full lowercase hexadecimal")
		}
		for _, c := range p.CommitSHA {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return errors.New("invalid commit SHA")
			}
		}
	}
	return nil
}
