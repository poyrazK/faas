package sharedruntime

import (
	"strconv"
	"strings"
)

// Shared runner bases for Node source apps.
const (
	RuntimeNode22 = "node22"
	RuntimeNode24 = "node24"
)

// nodeLTS maps the LTS codenames of the shared bases' majors.
var nodeLTS = map[string]string{"lts/jod": RuntimeNode22, "lts/krypton": RuntimeNode24, "lts/*": RuntimeNode24}

// NodeRuntimeForSpec picks the shared runner base for a Node version
// declaration (.nvmrc or engines.node, as written). No declaration means
// node24. A declaration either base satisfies picks it: "22", "22.x", "^22.11",
// "~22", "v22.11.0" and "lts/jod" pick node22; "24" and lower bounds such as
// ">=20" pick node24. Anything else returns "" so the build keeps its own
// toolchain: the builder pins Railpack's Node to the base's major, and a
// declared major must never be replaced.
func NodeRuntimeForSpec(spec string) string {
	spec = strings.ToLower(strings.TrimSpace(spec))
	if spec == "" {
		return RuntimeNode24
	}
	if runtime, ok := nodeLTS[spec]; ok {
		return runtime
	}
	op := ""
	for _, prefix := range []string{">=", ">", "^", "~", "=", "v"} {
		if strings.HasPrefix(spec, prefix) {
			op, spec = prefix, strings.TrimSpace(strings.TrimPrefix(spec, prefix))
			break
		}
	}
	digits, rest := spec, ""
	if i := strings.IndexFunc(spec, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		digits, rest = spec[:i], spec[i:]
	}
	major, err := strconv.Atoi(digits)
	if err != nil || !versionTail(rest) {
		return ""
	}
	switch op {
	case ">=":
		if major <= 24 {
			return RuntimeNode24
		}
	case ">":
		if major < 24 {
			return RuntimeNode24
		}
	default:
		switch major {
		case 22:
			return RuntimeNode22
		case 24:
			return RuntimeNode24
		}
	}
	return ""
}

// versionTail accepts what may follow a major: nothing, or dotted minor and
// patch parts that are numbers or wildcards. A compound range is rejected.
func versionTail(rest string) bool {
	if rest == "" {
		return true
	}
	if !strings.HasPrefix(rest, ".") {
		return false
	}
	for _, part := range strings.Split(rest[1:], ".") {
		if part == "x" || part == "*" {
			continue
		}
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return true
}
