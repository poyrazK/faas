package objectchecksum

import (
	"encoding/base64"
	"encoding/hex"
	"math/rand"
	"testing"
)

// adr: 1038
func TestFullChecksumCombinesVerifiedParts(t *testing.T) {
	for _, algorithm := range []string{"CRC32", "CRC32C", "CRC64NVME"} {
		t.Run(algorithm, func(t *testing.T) {
			rng := rand.New(rand.NewSource(17)) // Deterministic test bytes.
			for trial := 0; trial < 30; trial++ {
				whole := NewHash(algorithm)
				var parts []Part
				for n := 1; n <= trial%7+1; n++ {
					b := make([]byte, rng.Intn(4096)+1)
					_, _ = rng.Read(b)
					_, _ = whole.Write(b)
					h := NewHash(algorithm)
					_, _ = h.Write(b)
					parts = append(parts, Part{Number: int32(n), Size: int64(len(b)), Value: base64.StdEncoding.EncodeToString(h.Sum(nil))})
				}
				s, err := Build(Policy{algorithm, FullObject}, parts)
				if err != nil || s.Value != base64.StdEncoding.EncodeToString(whole.Sum(nil)) || !s.Complete() {
					t.Fatalf("trial %d: combined checksum %v, %v; direct %s", trial, s, err, base64.StdEncoding.EncodeToString(whole.Sum(nil)))
				}
			}
		})
	}
}

// adr: 1038
func TestCompositeChecksumIndependentVectors(t *testing.T) {
	// Literal vectors calculated with Python hashlib/zlib and AWS CRT, rather
	// than with Build. The selected bytes are "hello" followed by "world".
	for _, tc := range []struct{ algorithm, first, second, want string }{
		{"CRC32", "NhCmhg==", "OncRQw==", "wpn7tg==-2"},
		{"CRC32C", "mnG7TA==", "MaqBTg==", "u6ozzA==-2"},
		{"SHA1", "qvTGHdzF6KLavt4PO0gs2a6pQ00=", "fCEUM/AgcVl3Qeb/Wo6jR4mrv0M=", "znoNxIh8EVQQ1eN2GwlMi+XdFy4=-2"},
		{"SHA256", "LPJNul+wow4m6DsqxbninhsWHlwfp0JecwQzYpOLmCQ=", "SG6kYiTRu0+2gPNPfJrZao8k7Ii+c+qOWmxlJg6cuKc=", "cwXbmyq8zXBsJW2z2X5f9I1nfP5NOlkEr7faDjlQ4eI=-2"},
	} {
		s, err := Build(Policy{tc.algorithm, Composite}, []Part{{Number: 1, Size: 5, Value: tc.first}, {Number: 2, Size: 5, Value: tc.second}})
		if err != nil || s.Value != tc.want {
			t.Fatal(tc.algorithm, s.Value, err)
		}
	}
	h := NewHash("CRC64NVME")
	_, _ = h.Write([]byte("123456789"))
	if got := hex.EncodeToString(h.Sum(nil)); got != "ae8b14860a799888" {
		t.Fatal("CRC64/NVME AWS CRT check vector", got)
	}
}

func TestChecksumPoliciesAndComposite(t *testing.T) {
	for _, algorithm := range []string{"CRC32", "CRC32C", "SHA1", "SHA256"} {
		p, err := NewPolicy(algorithm, "")
		if err != nil || p.Type != Composite {
			t.Fatal(p, err)
		}
		h := NewHash(algorithm)
		_, _ = h.Write([]byte("part"))
		part := Part{Number: 1, Size: 4, Value: base64.StdEncoding.EncodeToString(h.Sum(nil))}
		s, err := Build(p, []Part{part})
		if err != nil || !s.Digest.Valid(1) || !s.Complete() {
			t.Fatal(s, err)
		}
		clone := s.Clone()
		clone.Parts[0].Size++
		if clone.Equal(s) {
			t.Fatal("mutable snapshot alias")
		}
		if _, err := Build(p, []Part{{Number: 2, Size: 4, Value: part.Value}}); err == nil {
			t.Fatal("nonconsecutive parts admitted")
		}
	}
	for _, p := range []Policy{{"SHA256", FullObject}, {"CRC64NVME", Composite}, {"", Composite}, {"unknown", FullObject}} {
		if p.Valid() {
			t.Fatal("invalid policy accepted", p)
		}
	}
}
