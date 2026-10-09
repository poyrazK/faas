// Package routepolicy holds dependency-free managed outbound route rules.
package routepolicy

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
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
	if path == "" || path[0] != '/' || len(path) > MaxRequestLength || strings.ContainsAny(path, "\\;#?") || strings.Contains(path, "//") {
		return false
	}
	for _, segment := range strings.Split(path[1:], "/") {
		decoded, err := url.PathUnescape(segment)
		if err != nil || !utf8.ValidString(decoded) || decoded == "." || decoded == ".." || strings.ContainsAny(decoded, "/\\;#?%") {
			return false
		}
		for i := range len(decoded) {
			if decoded[i] < 0x20 || decoded[i] == 0x7f {
				return false
			}
		}
		for i := 0; i < len(segment); i++ {
			if segment[i] == '%' {
				if i+2 >= len(segment) || !upperHex(segment[i+1]) || !upperHex(segment[i+2]) {
					return false
				}
				decodedByte := hexByte(segment[i+1], segment[i+2])
				if unreservedPathByte(decodedByte) {
					return false
				}
				i += 2
				continue
			}
		}
	}
	for i := 0; i < len(path); i++ {
		if path[i] < 0x21 || path[i] > 0x7e {
			return false
		}
	}
	return true
}

func upperHex(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'A' && value <= 'F'
}

func hexByte(high, low byte) byte {
	decode := func(value byte) byte {
		if value >= '0' && value <= '9' {
			return value - '0'
		}
		return value - 'A' + 10
	}
	return decode(high)<<4 | decode(low)
}

func unreservedPathByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || strings.ContainsRune("-._~", rune(value))
}
