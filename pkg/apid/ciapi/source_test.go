package ciapi_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/ciapi"
)

type rpcTestBody struct {
	io.Reader
	closed  int
	onClose func()
}

func (b *rpcTestBody) Close() error { b.closed++; b.onClose(); return nil }

func TestCISourceRPCRequiresFinalReceiptBeforeEOF(t *testing.T) {
	sha := strings.Repeat("a", 40)
	for _, name := range []string{"valid", "missing_sha", "wrong_sha", "truncated", "trailer_error", "read_error"} {
		t.Run(name, func(t *testing.T) {
			var resolved string
			var truncated bool
			var trailerErr error
			body := &rpcTestBody{Reader: strings.NewReader("opaque archive"), onClose: func() {
				resolved = sha
				switch name {
				case "missing_sha":
					resolved = ""
				case "wrong_sha":
					resolved = strings.Repeat("b", 40)
				case "truncated":
					truncated = true
				case "trailer_error":
					trailerErr = errors.New("provider unavailable")
				}
			}}
			if name == "read_error" {
				body.Reader = io.MultiReader(strings.NewReader("prefix"), errorReader{})
			}
			source := ciapi.RPCSource{Open: func(context.Context, string, int64, string, string, int64) (io.ReadCloser, func() (string, bool, error), error) {
				return body, func() (string, bool, error) { return resolved, truncated, trailerErr }, nil
			}}
			reader, _, _, _, err := source.StreamSourceRef(t.Context(), "account", 12, "gregale/api", sha, 1024)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(reader)
			_ = reader.Close()
			if name == "valid" && (err != nil || !bytes.Equal(data, []byte("opaque archive"))) {
				t.Fatal("valid final receipt rejected")
			}
			if name != "valid" && err == nil {
				t.Fatal("unverified source reached EOF")
			}
			if body.closed != 1 {
				t.Fatal("source stream was not closed exactly once")
			}
		})
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("incomplete RPC") }
