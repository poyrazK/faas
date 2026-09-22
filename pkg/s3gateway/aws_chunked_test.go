package s3gateway

import (
	"encoding/hex"
	"testing"
)

func TestAWSChecksumAlgorithmsMatchPublishedCheckValues(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		want string
	}{
		{name: "x-amz-checksum-crc32", want: "cbf43926"},
		{name: "x-amz-checksum-crc32c", want: "e3069283"},
		{name: "x-amz-checksum-crc64nvme", want: "ae8b14860a799888"},
		{name: "x-amz-checksum-sha1", want: "f7c3bc1d808e04732adf679965ccc34ca7ae3441"},
		{name: "x-amz-checksum-sha256", want: "15e2b0d3c33891ebb0f1ef609ec419420c20e320ce94c65fbc8c3312448eb225"},
	} {
		t.Run(test.name, func(t *testing.T) {
			digest := newAWSChecksum(test.name)
			if digest == nil {
				t.Fatal("checksum algorithm is not registered")
			}
			_, _ = digest.Write([]byte("123456789"))
			if got := hex.EncodeToString(digest.Sum(nil)); got != test.want {
				t.Fatalf("checksum = %s, want %s", got, test.want)
			}
		})
	}
}
