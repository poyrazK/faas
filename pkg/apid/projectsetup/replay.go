package projectsetup

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
)

// Replay streams an account-owned cached archive through the ordinary upload
// path. Metadata, including initial secret values, stays in memory; only the
// source file is ever spooled by the scanner. Close the request on every path
// so an early validation failure cannot leave the producer blocked.
func Replay(original *http.Request, sourcePath string, fields map[string]string) (*http.Request, func(), error) {
	source, err := os.Open(sourcePath) //nolint:forbidigo // internal account-owned cache path, never a caller path
	if err != nil {
		return nil, nil, fmt.Errorf("open reviewed source: %w", err)
	}
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	go func() {
		defer func() { _ = source.Close() }()
		err := writeReplay(form, source, fields)
		if err == nil {
			err = form.Close()
		}
		_ = writer.CloseWithError(err)
	}()
	req := original.Clone(original.Context())
	req.Body = reader
	req.ContentLength = -1
	req.Header.Set("Content-Type", form.FormDataContentType())
	cleanup := func() { _ = reader.Close(); _ = source.Close() }
	return req, cleanup, nil
}

func writeReplay(form *multipart.Writer, source io.Reader, fields map[string]string) error {
	for _, key := range sortedKeys(fields) {
		if err := form.WriteField(key, fields[key]); err != nil {
			return fmt.Errorf("write project field: %w", err)
		}
	}
	part, err := form.CreateFormFile("source", "reviewed-source.tar.gz")
	if err != nil {
		return fmt.Errorf("create project source: %w", err)
	}
	_, err = io.Copy(part, source)
	if err != nil {
		return fmt.Errorf("stream reviewed source: %w", err)
	}
	return nil
}
