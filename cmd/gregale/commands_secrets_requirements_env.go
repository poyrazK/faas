package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Extract names only. Values are opaque text: never evaluate, unquote, expand,
// return, or include them in diagnostics.
func readRequiredSecretEnvNames(path string) ([]string, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return nil, errors.New("could not read dotenv source")
	}
	if len(data) > 1<<20 {
		return nil, errors.New("dotenv source exceeds 1 MiB")
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 64*1024)
	keys := []string{}
	seen := map[string]bool{}
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.ContainsRune(line, '\x00') {
			return nil, fmt.Errorf("dotenv line %d contains a NUL byte", lineNumber)
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, _, assignment := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !assignment || api.ValidateSecretKey(key) != nil {
			return nil, fmt.Errorf("dotenv line %d must contain a valid KEY=VALUE assignment", lineNumber)
		}
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	if scanner.Err() != nil {
		return nil, errors.New("dotenv source has an unreadable or oversized line (limit 64 KiB)")
	}
	if len(keys) == 0 {
		return nil, errors.New("dotenv source contains no key assignments")
	}
	return keys, nil
}
