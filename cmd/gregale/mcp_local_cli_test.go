// adr: 20261026141841
package main

import (
	"slices"
	"testing"
)

func TestEnvironWithoutDropsOnlyNamedVariables(t *testing.T) {
	got := environWithout([]string{"FAAS_TOKEN=old", "FAAS_TOKEN_FILE=x", "PATH=/bin", "FAAS_API=http://old"}, "FAAS_TOKEN", "FAAS_API")
	if want := []string{"FAAS_TOKEN_FILE=x", "PATH=/bin"}; !slices.Equal(got, want) {
		t.Fatalf("environWithout = %v, want %v", got, want)
	}
}

func TestCappedBufferKeepsThePrefixAndAcceptsEverything(t *testing.T) {
	b := &cappedBuffer{limit: 5}
	for _, chunk := range []string{"abc", "defgh", "ij"} {
		if n, err := b.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	if b.String() != "abcde" {
		t.Fatalf("buffer = %q, want the first five bytes", b.String())
	}
}
