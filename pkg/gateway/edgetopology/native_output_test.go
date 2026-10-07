package edgetopology

// adr: 620

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestNativeSystemdOutputBoundCannotBeBypassedByIOCopy(t *testing.T) {
	limit := api.RuntimeUpgradeNativeUnitMaxBytes
	for _, extra := range []int{0, 1, limit} {
		output := nativeBoundedOutput{limit: limit}
		// LimitedReader intentionally lacks WriterTo, exercising io.Copy's
		// destination ReaderFrom dispatch as used by os/exec's output copier.
		reader := &io.LimitedReader{R: strings.NewReader(strings.Repeat("x", limit+extra)), N: int64(limit + extra)}
		n, err := io.Copy(&output, reader)
		if extra == 0 {
			if err != nil || n != int64(limit) || len(output.Bytes()) != limit {
				t.Fatal(n, err, len(output.Bytes()))
			}
		} else if !errors.Is(err, ErrNativeUnverified) || len(output.Bytes()) > limit {
			t.Fatal(n, err, len(output.Bytes()))
		}
	}
	output := nativeBoundedOutput{limit: limit}
	if _, err := output.Write(make([]byte, limit)); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte("overflow")); !errors.Is(err, ErrNativeUnverified) || len(output.Bytes()) != limit {
		t.Fatal(err)
	}
}
