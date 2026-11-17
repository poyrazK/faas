package gregalemanifest

import (
	"regexp"
	"slices"
	"strings"
)

// Snapshot hook names as the platform names them (ADR-20261110190000).
const (
	FunctionHookInit           = "init"
	FunctionHookBeforeSnapshot = "before_snapshot"
	FunctionHookAfterRestore   = "after_restore"
)

var (
	jsHookNames = map[string]string{"init": FunctionHookInit, "beforeSnapshot": FunctionHookBeforeSnapshot, "afterRestore": FunctionHookAfterRestore}
	// export function init(...) / export async function afterRestore(...) /
	// export const beforeSnapshot = ...
	jsNamedHookRE = regexp.MustCompile(`(?m)^\s*export\s+(?:(?:async\s+)?function\s*\*?\s*|(?:const|let|var)\s+)(init|beforeSnapshot|afterRestore)\b`)
	jsDefaultRE   = regexp.MustCompile(`(?m)^\s*export\s+default\s*\{`)
	// Module-level def init / async def after_restore, at column 0.
	pyHookRE = regexp.MustCompile(`(?m)^(?:async\s+)?def\s+(init|before_snapshot|after_restore)\s*\(`)
)

// DetectFunctionHooks returns the snapshot hooks a Node or Python handler
// declares, read without running it (ADR-20261110190000 §5): named exports
// or methods of an `export default { ... }` object literal in Node, and
// module-level functions in Python. A hook built at runtime is not seen. The
// result is sorted in hook order (init, before_snapshot, after_restore); a
// file in another language declares none.
func DetectFunctionHooks(rel string, src []byte) []string {
	lang, ok := inlineLang(rel)
	if !ok {
		return nil
	}
	found := map[string]bool{}
	text := string(src)
	if lang == langPython {
		for _, m := range pyHookRE.FindAllStringSubmatch(text, -1) {
			found[m[1]] = true
		}
	} else {
		for _, m := range jsNamedHookRE.FindAllStringSubmatch(text, -1) {
			found[jsHookNames[m[1]]] = true
		}
		if loc := jsDefaultRE.FindStringIndex(text); loc != nil {
			for _, name := range jsObjectMembers(text, loc[1]) {
				if hook, ok := jsHookNames[name]; ok {
					found[hook] = true
				}
			}
		}
	}
	var hooks []string
	for _, name := range []string{FunctionHookInit, FunctionHookBeforeSnapshot, FunctionHookAfterRestore} {
		if found[name] {
			hooks = append(hooks, name)
		}
	}
	return hooks
}

// jsObjectMembers lists the top-level member names of the object literal
// whose body starts at pos (just after its `{`): methods (`name(`,
// `async name(`), properties (`name:`) and shorthand (`name,`). Strings, template
// literals and comments are skipped; nested braces are not descended into.
func jsObjectMembers(src string, pos int) []string {
	var names []string
	depth := 1
	expectKey := true
	for i := pos; i < len(src) && depth > 0; {
		c := src[i]
		switch {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return names
			}
			i += end + 4
			continue
		case c == '"' || c == '\'' || c == '`':
			i = skipJSString(src, i)
			continue
		case c == '{' || c == '(' || c == '[':
			depth++
		case c == '}' || c == ')' || c == ']':
			depth--
		case c == ',' && depth == 1:
			expectKey = true
		case depth == 1 && expectKey && isIdentStart(rune(c)):
			start := i
			for i < len(src) && isIdentPart(rune(src[i])) {
				i++
			}
			word := src[start:i]
			if word == "async" || word == "get" || word == "set" || word == "static" {
				continue
			}
			rest := strings.TrimLeft(src[i:], " \t\r\n")
			// method (`name(`), property (`name:`) or shorthand (`name,` / `name }`)
			if strings.HasPrefix(rest, "(") || strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, ",") || strings.HasPrefix(rest, "}") {
				names = append(names, word)
			}
			expectKey = false
			continue
		case c == '*' && depth == 1:
			// generator method: *name()
		case c != ' ' && c != '\t' && c != '\n' && c != '\r':
			if depth == 1 {
				expectKey = false
			}
		}
		i++
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// skipJSString returns the index just past the string literal starting at i.
func skipJSString(src string, i int) int {
	quote := src[i]
	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\\':
			j++
		case quote:
			return j + 1
		}
	}
	return len(src)
}
