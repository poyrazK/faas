//go:build linux

package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// Short payloads are valid on the resume wire. Successful diagnostics used
// to slice entropy[:8], panicking before reseeding for lengths 1 through 7.
func TestAddHostEntropyAcceptsShortPayloads(t *testing.T) {
	for _, size := range []int{0, 1, 7, 8, 256} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			entropy := make([]byte, size)
			if _, err := rand.Read(entropy); err != nil {
				t.Fatal(err)
			}
			if err := addHostEntropy(entropy); err != nil {
				// Unprivileged host tests may mix input but cannot rekey the
				// global CRNG. Production guest PID 1 must succeed; metal
				// tests check that two restored guests produce distinct UUIDs.
				if errors.Is(err, unix.EPERM) && strings.Contains(err.Error(), "RNDRESEEDCRNG") {
					return
				}
				t.Fatal(err)
			}
		})
	}
	if err := addHostEntropy(make([]byte, 4097)); err == nil {
		t.Fatal("oversized entropy must be rejected")
	}
}

func TestReseedCRNGFailsClosed(t *testing.T) {
	if err := reseedCRNG(-1); !errors.Is(err, unix.EBADF) {
		t.Fatalf("invalid entropy device error = %v, want EBADF", err)
	}
}
