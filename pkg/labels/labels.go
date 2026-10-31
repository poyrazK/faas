// Package labels validates resource labels, applies label changes and parses
// and matches label selectors (ADR-20261031100025). Labels and selectors use
// Kubernetes syntax, so a selector copied from kubectl works. Every function is
// pure: the API, the CLI, the importer and tests share it, and nothing here
// reads or writes state.
package labels

import (
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// ReservedPrefix is the key prefix that belongs to Gregale. Its subdomains
// (for example "build.gregale.dev/") are reserved too. Customers cannot set or
// remove such keys; Gregale may set them.
const ReservedPrefix = "gregale.dev"

// Stable error codes. The API returns them as the RFC 7807 code.
const (
	CodeInvalidKey      = "invalid_label_key"
	CodeInvalidValue    = "invalid_label_value"
	CodeReservedKey     = "reserved_label_key"
	CodeInvalidChange   = "invalid_label_change"
	CodeLimitExceeded   = "label_limit_exceeded"
	CodeInvalidSelector = "invalid_label_selector"
)

// Error is a label or selector the package refused. Key names the label key or
// the selector requirement at fault; Limit and Observed are set when a count
// or length limit was passed.
type Error struct {
	Code     string
	Key      string
	Message  string
	Limit    int
	Observed int
}

func (e *Error) Error() string {
	if e.Key == "" {
		return e.Message
	}
	return fmt.Sprintf("%q: %s", e.Key, e.Message)
}

// ValidateKey reports whether key is a Kubernetes label key: an optional
// DNS-subdomain prefix and a slash, then a name of 1–63 characters of
// [A-Za-z0-9._-] that starts and ends alphanumeric.
func ValidateKey(key string) error {
	prefix, name, hasPrefix := strings.Cut(key, "/")
	if !hasPrefix {
		name, prefix = prefix, ""
	}
	switch {
	case hasPrefix && prefix == "":
		return keyError(key, "the prefix before / is empty")
	case len(prefix) > api.LabelKeyPrefixMax:
		return &Error{Code: CodeInvalidKey, Key: key, Message: fmt.Sprintf("the prefix is %d characters; the limit is %d", len(prefix), api.LabelKeyPrefixMax), Limit: api.LabelKeyPrefixMax, Observed: len(prefix)}
	case hasPrefix && !dnsSubdomain(prefix):
		return keyError(key, "the prefix must be a lowercase DNS subdomain such as example.com")
	case name == "":
		return keyError(key, "the name is empty")
	case len(name) > api.LabelKeyNameMax:
		return &Error{Code: CodeInvalidKey, Key: key, Message: fmt.Sprintf("the name is %d characters; the limit is %d", len(name), api.LabelKeyNameMax), Limit: api.LabelKeyNameMax, Observed: len(name)}
	case !labelToken(name):
		return keyError(key, "the name must be letters, digits, '.', '_' or '-', starting and ending with a letter or digit")
	}
	return nil
}

// ValidateValue reports whether value is a Kubernetes label value: empty, or
// 1–63 characters of [A-Za-z0-9._-] that start and end alphanumeric.
func ValidateValue(key, value string) error {
	if value == "" {
		return nil
	}
	if len(value) > api.LabelValueMax {
		return &Error{Code: CodeInvalidValue, Key: key, Message: fmt.Sprintf("the value is %d characters; the limit is %d", len(value), api.LabelValueMax), Limit: api.LabelValueMax, Observed: len(value)}
	}
	if !labelToken(value) {
		return &Error{Code: CodeInvalidValue, Key: key, Message: "the value must be empty or letters, digits, '.', '_' or '-', starting and ending with a letter or digit"}
	}
	return nil
}

// Reserved reports whether key carries the gregale.dev prefix or one of its
// subdomains.
func Reserved(key string) bool {
	prefix, _, ok := strings.Cut(key, "/")
	return ok && (prefix == ReservedPrefix || strings.HasSuffix(prefix, "."+ReservedPrefix))
}

// Validate checks a full label map: its size, every key and every value.
// Reserved keys pass, because Gregale writes them; customer writes go through
// Change.Validate.
func Validate(labels map[string]string) error {
	if len(labels) > api.LabelsPerResourceMax {
		return limitError(len(labels))
	}
	for key, value := range labels {
		if err := ValidateKey(key); err != nil {
			return err
		}
		if err := ValidateValue(key, value); err != nil {
			return err
		}
	}
	return nil
}

// Change is a customer's label edit: keys to set and keys to remove, applied
// together. Setting a key to its current value and removing an absent key
// change nothing, so a Change is idempotent.
type Change struct {
	Set    map[string]string `json:"set,omitempty"`
	Remove []string          `json:"remove,omitempty"`
}

// Validate checks a customer's change on its own: valid keys and values, no
// reserved key, no key both set and removed, and no more keys than a resource
// can carry.
func (c Change) Validate() error {
	if n := max(len(c.Set), len(c.Remove)); n > api.LabelsPerResourceMax {
		return limitError(n)
	}
	for key, value := range c.Set {
		if err := customerKey(key); err != nil {
			return err
		}
		if err := ValidateValue(key, value); err != nil {
			return err
		}
	}
	for _, key := range c.Remove {
		if err := customerKey(key); err != nil {
			return err
		}
		if _, ok := c.Set[key]; ok {
			return &Error{Code: CodeInvalidChange, Key: key, Message: "the key is both set and removed"}
		}
	}
	return nil
}

// Apply validates c and returns the labels current would carry after it. It
// does not modify current. The result is refused when it would pass
// api.LabelsPerResourceMax.
func Apply(current map[string]string, c Change) (map[string]string, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	next := make(map[string]string, len(current)+len(c.Set))
	for key, value := range current {
		next[key] = value
	}
	for _, key := range c.Remove {
		delete(next, key)
	}
	for key, value := range c.Set {
		next[key] = value
	}
	if len(next) > api.LabelsPerResourceMax {
		return nil, limitError(len(next))
	}
	return next, nil
}

func customerKey(key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	if Reserved(key) {
		return &Error{Code: CodeReservedKey, Key: key, Message: "keys under " + ReservedPrefix + "/ belong to Gregale"}
	}
	return nil
}

func keyError(key, message string) error {
	return &Error{Code: CodeInvalidKey, Key: key, Message: message}
}

func limitError(observed int) error {
	return &Error{
		Code:     CodeLimitExceeded,
		Message:  fmt.Sprintf("a resource carries at most %d labels; this would be %d", api.LabelsPerResourceMax, observed),
		Limit:    api.LabelsPerResourceMax,
		Observed: observed,
	}
}

// labelToken reports whether s is 1+ characters of [A-Za-z0-9._-] starting
// and ending alphanumeric.
func labelToken(s string) bool {
	if s == "" || !alnum(s[0]) || !alnum(s[len(s)-1]) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; !alnum(c) && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// dnsSubdomain reports whether s is a lowercase RFC 1123 subdomain: dot-separated
// labels of [a-z0-9-] that start and end alphanumeric, each at most 63 characters.
func dnsSubdomain(s string) bool {
	for _, part := range strings.Split(s, ".") {
		if part == "" || len(part) > 63 || !lowerAlnum(part[0]) || !lowerAlnum(part[len(part)-1]) {
			return false
		}
		for i := 0; i < len(part); i++ {
			if c := part[i]; !lowerAlnum(c) && c != '-' {
				return false
			}
		}
	}
	return true
}

func alnum(c byte) bool {
	return lowerAlnum(c) || 'A' <= c && c <= 'Z'
}

func lowerAlnum(c byte) bool {
	return 'a' <= c && c <= 'z' || '0' <= c && c <= '9'
}
