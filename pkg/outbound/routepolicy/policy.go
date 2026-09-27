// Package routepolicy holds dependency-free managed outbound route rules.
package routepolicy

import (
	"errors"
	"fmt"
	"strings"
)

const (
	MaxMethods       = 6
	MaxPaths         = 32
	MaxPrefixLength  = 512
	MaxRequestLength = 2048
)

var ErrInvalid = errors.New("invalid outbound route policy")

var supportedMethods = map[string]struct{}{
	"GET": {}, "HEAD": {}, "POST": {}, "PUT": {}, "PATCH": {}, "DELETE": {},
}

type Policy struct {
	AllowedMethods      []string
	AllowedPathPrefixes []string
}

func Validate(policy Policy) error {
	if len(policy.AllowedMethods) == 0 || len(policy.AllowedMethods) > MaxMethods ||
		len(policy.AllowedPathPrefixes) == 0 || len(policy.AllowedPathPrefixes) > MaxPaths {
		return fmt.Errorf("%w: permissions must be nonempty and bounded", ErrInvalid)
	}
	seenMethods := make(map[string]struct{}, len(policy.AllowedMethods))
	for _, method := range policy.AllowedMethods {
		if _, ok := supportedMethods[method]; !ok {
			return fmt.Errorf("%w: unsupported method", ErrInvalid)
		}
		if _, exists := seenMethods[method]; exists {
			return fmt.Errorf("%w: duplicate method", ErrInvalid)
		}
		seenMethods[method] = struct{}{}
	}
	seenPaths := make(map[string]struct{}, len(policy.AllowedPathPrefixes))
	for _, prefix := range policy.AllowedPathPrefixes {
		if !CanonicalPath(prefix) || len(prefix) > MaxPrefixLength || (prefix != "/" && strings.HasSuffix(prefix, "/")) {
			return fmt.Errorf("%w: path prefix is not canonical", ErrInvalid)
		}
		if _, exists := seenPaths[prefix]; exists {
			return fmt.Errorf("%w: duplicate path prefix", ErrInvalid)
		}
		seenPaths[prefix] = struct{}{}
	}
	return nil
}

// ValidateSubset requires an explicit policy contained within the current
// operator ceiling. The gateway also intersects both policies at request time.
func ValidateSubset(ceiling, requested Policy) error {
	if err := Validate(requested); err != nil {
		return err
	}
	methods := make(map[string]struct{}, len(ceiling.AllowedMethods))
	for _, method := range ceiling.AllowedMethods {
		methods[method] = struct{}{}
	}
	for _, method := range requested.AllowedMethods {
		if _, allowed := methods[method]; !allowed {
			return fmt.Errorf("%w: method exceeds integration policy", ErrInvalid)
		}
	}
	for _, path := range requested.AllowedPathPrefixes {
		allowed := false
		for _, prefix := range ceiling.AllowedPathPrefixes {
			if matchesPrefix(path, prefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("%w: path exceeds integration policy", ErrInvalid)
		}
	}
	return nil
}

func (p Policy) AllowsRequest(method, escapedPath string) bool {
	if !CanonicalPath(escapedPath) {
		return false
	}
	methodAllowed := false
	for _, allowed := range p.AllowedMethods {
		if method == allowed {
			methodAllowed = true
			break
		}
	}
	if !methodAllowed {
		return false
	}
	for _, prefix := range p.AllowedPathPrefixes {
		if matchesPrefix(escapedPath, prefix) {
			return true
		}
	}
	return false
}

func matchesPrefix(path, prefix string) bool {
	return prefix == "/" || path == prefix || strings.HasPrefix(path, prefix+"/")
}

func CanonicalPath(path string) bool {
	if path == "" || path[0] != '/' || len(path) > MaxRequestLength || strings.ContainsAny(path, "%\\;#?") || strings.Contains(path, "//") {
		return false
	}
	for _, segment := range strings.Split(path[1:], "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	for i := 0; i < len(path); i++ {
		if path[i] < 0x21 || path[i] > 0x7e {
			return false
		}
	}
	return true
}
