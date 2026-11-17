package utf8chunk_test

import (
	"testing"

	"github.com/onebox-faas/faas/internal/utf8chunk"
)

func TestPrefixKeepsCompleteRunesAndBuffersIncompleteOnes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input []byte
		limit int
		final bool
		want  int
	}{
		{"whole rune", []byte("a€"), 4, false, 4},
		{"boundary inside rune", []byte("a€b"), 3, false, 1},
		{"incomplete frame", []byte{'a', 0xe2, 0x82}, 8, false, 1},
		{"incomplete final frame", []byte{0xe2, 0x82}, 8, true, 2},
		{"invalid byte is complete", []byte{0xff}, 8, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := utf8chunk.Prefix(tc.input, tc.limit, tc.final); got != tc.want {
				t.Fatalf("prefix=%d want=%d", got, tc.want)
			}
		})
	}
}
