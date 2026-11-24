package verifycontract

// ADR-20261122220000 amendment 2: an HTTP check's header value may reference
// one of the app's own secrets as ${secret:NAME}. The runner never sees the
// value: imaged asks secretd for exactly the names the stored contract
// references, in the deployment's scope, and substitutes them just before the
// request leaves for the candidate, which already holds them as env. Reports
// carry names, never values.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// secretRefPattern matches one reference. Anything between the braces is
// captured so an invalid name is reported instead of sent literally.
var secretRefPattern = regexp.MustCompile(`\$\{secret:([^}]*)\}`)

// secretRefMarker starts a reference; it is refused where no substitution
// happens, so a misplaced reference is an error rather than literal text.
const secretRefMarker = "${secret:"

// headerSecretRefs returns the names value references, validating each.
func headerSecretRefs(value string) ([]string, error) {
	matches := secretRefPattern.FindAllStringSubmatch(value, -1)
	if len(matches) == 0 {
		if strings.Contains(value, secretRefMarker) {
			return nil, errors.New("a ${secret:NAME} reference is not closed")
		}
		return nil, nil
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		if err := api.ValidateEnvKey(m[1]); err != nil {
			return nil, fmt.Errorf("${secret:%s} does not name a valid secret key", m[1])
		}
		names = append(names, m[1])
	}
	if strings.Contains(secretRefPattern.ReplaceAllString(value, ""), secretRefMarker) {
		return nil, errors.New("a ${secret:NAME} reference is not closed")
	}
	return names, nil
}

// SecretRefs returns the distinct secret names the contract's HTTP check
// headers reference, sorted. The contract must be valid.
func (c Contract) SecretRefs() []string {
	seen := map[string]bool{}
	for _, check := range c.Checks {
		if check.HTTP == nil {
			continue
		}
		for _, value := range check.HTTP.Headers {
			names, _ := headerSecretRefs(value)
			for _, n := range names {
				seen[n] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// validateSecretRefs checks the contract-wide reference bound.
func (c Contract) validateSecretRefs() error {
	if n := len(c.SecretRefs()); n > MaxSecretRefs {
		return invalidf("check headers reference %d secrets, more than %d", n, MaxSecretRefs)
	}
	return nil
}

// misplacedSecretRef reports a ${secret: marker anywhere in the check other
// than its HTTP header values, where it would be sent literally.
func misplacedSecretRef(check Check) bool {
	if check.HTTP != nil {
		stripped := *check.HTTP
		stripped.Headers = nil
		check.HTTP = &stripped
	}
	raw, err := json.Marshal(check)
	return err == nil && bytes.Contains(raw, []byte(secretRefMarker))
}

// MissingSecretError names a referenced secret the deployment's scope does
// not hold.
type MissingSecretError struct{ Name string }

func (e *MissingSecretError) Error() string {
	return fmt.Sprintf("secret %s is not set in this deployment's scope", e.Name)
}

// ExpandSecrets replaces every ${secret:NAME} in value with values[NAME]. A
// name values lacks is a *MissingSecretError.
func ExpandSecrets(value string, values map[string]string) (string, error) {
	var missing string
	out := secretRefPattern.ReplaceAllStringFunc(value, func(ref string) string {
		name := secretRefPattern.FindStringSubmatch(ref)[1]
		v, ok := values[name]
		if !ok {
			if missing == "" {
				missing = name
			}
			return ""
		}
		return v
	})
	if missing != "" {
		return "", &MissingSecretError{Name: missing}
	}
	return out, nil
}
