//go:build linux

package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestIdentityFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := writeIdentityFrame(&buf, []byte(`{"audience":"sts.amazonaws.com"}`)); err != nil {
		t.Fatal(err)
	}
	got, err := readIdentityFrameConn(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"audience":"sts.amazonaws.com"}` {
		t.Fatalf("body = %q", got)
	}
}

func TestIdentityFrameRejectsOversized(t *testing.T) {
	if err := writeIdentityFrame(&bytes.Buffer{}, bytes.Repeat([]byte{'x'}, workloadIdentityMaxFrame+1)); err == nil {
		t.Fatal("oversized request accepted")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], workloadIdentityMaxFrame+1)
	if _, err := readIdentityFrameConn(bytes.NewReader(header[:])); err == nil {
		t.Fatal("oversized response accepted")
	}
}

func TestStampWorkloadIdentityEnv(t *testing.T) {
	got := StampWorkloadIdentityEnv([]string{"FAAS_WORKLOAD_IDENTITY_ENDPOINT=attacker"})
	if got[len(got)-1] != "FAAS_WORKLOAD_IDENTITY_ENDPOINT="+workloadIdentityEndpoint {
		t.Fatalf("endpoint env = %q", got[len(got)-1])
	}
}
