package reposcan

import (
	"errors"
	"strings"
)

// MaxSlugPrefixLen leaves room for a service name of at least three
// characters inside an app slug's 40.
const MaxSlugPrefixLen = 30

// ValidateSlugPrefix checks an app-name prefix (Compose
// x-gregale-slug-prefix or `deploy --project --prefix`): lowercase letters,
// digits and hyphens, starting with a letter. "" is no prefix.
func ValidateSlugPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if len(prefix) > MaxSlugPrefixLen {
		return errors.New("must be at most 30 characters")
	}
	if prefix[0] < 'a' || prefix[0] > 'z' {
		return errors.New("must start with a lowercase letter")
	}
	if strings.Contains(prefix, "--") {
		return errors.New("must not contain two hyphens in a row")
	}
	for _, r := range prefix {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return errors.New("must contain only lowercase letters, digits and hyphens")
		}
	}
	return nil
}

// ApplySlugPrefix sets prefix on every workload, replacing a Compose-declared
// one; `deploy --project --prefix` wins over the file.
func ApplySlugPrefix(workloads []Workload, prefix string) {
	if prefix == "" {
		return
	}
	for i := range workloads {
		workloads[i].SlugPrefix = prefix
	}
}
