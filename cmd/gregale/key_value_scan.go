package main

import (
	"bufio"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// keyValueLineMaxBytes bounds one KEY=VALUE line read from stdin or a file by
// secrets set, secrets rotate and env push: the largest plan's secret value
// cap plus room for the key.
const keyValueLineMaxBytes = 64 * 1024

// newKeyValueScanner reads KEY=VALUE lines up to keyValueLineMaxBytes.
func newKeyValueScanner(scanner *bufio.Scanner) *bufio.Scanner {
	scanner.Buffer(make([]byte, 0, keyValueLineMaxBytes), keyValueLineMaxBytes)
	return scanner
}

// keyValueScanErr names the limit when a line is over keyValueLineMaxBytes,
// which bufio otherwise reports as "token too long" before the API can say
// what the cap is (H10-66).
func keyValueScanErr(err error) error {
	if !errors.Is(err, bufio.ErrTooLong) {
		return err
	}
	return fmt.Errorf("a KEY=VALUE line is longer than %d KiB; secret values are capped at %d bytes on the largest plan (https://gregale.dev/docs/secrets#limits)",
		keyValueLineMaxBytes/1024, api.MustLimitsFor(api.PlanScale).SecretValueMaxBytes)
}
