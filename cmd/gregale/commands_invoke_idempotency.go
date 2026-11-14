package main

import (
	"errors"
	"flag"
	"strings"
)

// Only the async endpoint has an idempotency receipt. An explicitly empty key
// must fail rather than silently mint a different identity on every CLI run.
func invokeIdempotencyKey(fs *flag.FlagSet, async bool, key string) (string, error) {
	provided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "idempotency-key" {
			provided = true
		}
	})
	if !provided {
		return "", nil
	}
	if !async {
		return "", errors.New("requires --async or --wait")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("must not be empty")
	}
	return key, validateDeployIdempotencyKey(key)
}
