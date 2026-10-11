package api

import (
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strings"
)

// FunctionHandlerFileMaxBytes bounds a function handler file path.
const FunctionHandlerFileMaxBytes = 256

var functionHandlerFileRE = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// FunctionHandlerProblem validates a function deploy's handler: it must be
// set, and a handler naming a source file must stay inside the source root
// (ADR-985). Nil means acceptable.
func FunctionHandlerProblem(runtime, handler string) *Problem {
	if handler == "" {
		return ErrHandlerMissing()
	}
	if _, _, err := FunctionHandlerFile(runtime, handler); err != nil {
		return NewProblem(http.StatusBadRequest, CodeValidation, "Invalid handler", err.Error())
	}
	return nil
}

// FunctionHandlerFile reports whether handler names a source file for a
// function runtime (ADR-985 shared-tree functions) and returns its clean path
// relative to the source root. Node runtimes take a .js or .mjs file and
// Python runtimes a .py file. Any other value, such as the historical
// "handler.handler", keeps the runtime's default handler file, as do the Go
// and binary runtimes, whose handler is the built executable.
func FunctionHandlerFile(runtime, handler string) (string, bool, error) {
	handler = strings.TrimSpace(handler)
	var exts []string
	switch {
	case strings.HasPrefix(runtime, "node"):
		exts = []string{".js", ".mjs"}
	case strings.HasPrefix(runtime, "python"):
		exts = []string{".py"}
	default:
		return "", false, nil
	}
	ext := path.Ext(handler)
	matched := false
	for _, candidate := range exts {
		matched = matched || ext == candidate
	}
	if !matched {
		return "", false, nil
	}
	if len(handler) > FunctionHandlerFileMaxBytes {
		return "", false, fmt.Errorf("handler path is longer than %d bytes", FunctionHandlerFileMaxBytes)
	}
	if !functionHandlerFileRE.MatchString(handler) {
		return "", false, fmt.Errorf("handler %q may contain only letters, digits, '.', '_', '-' and '/'", handler)
	}
	clean := path.Clean(handler)
	if strings.HasPrefix(handler, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false, errors.New("handler must be a path inside the source root")
	}
	return clean, true, nil
}
