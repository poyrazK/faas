package objectqualification

import (
	"bytes"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

// Keep complete JSONL rows within the existing soak timeline/row budgets.
// Truncation is explicit and never stops replication or grants durability proof.
type replicationProgressWriter struct {
	output    io.Writer
	line      []byte
	bytes     int
	truncated bool
}

func (w *replicationProgressWriter) Write(body []byte) (int, error) {
	const marker = "{\"truncated\":true}\n"
	n := len(body)
	for len(body) > 0 && !w.truncated {
		end := bytes.IndexByte(body, '\n')
		if end < 0 {
			end = len(body) - 1
		}
		part := body[:end+1]
		if len(w.line)+len(part) > api.ObjectQualificationSoakWindowBytes || w.bytes+len(w.line)+len(part)+len(marker) > api.ObjectQualificationSoakTimelineBytes {
			w.truncated, w.line = true, nil
			_, err := io.WriteString(w.output, marker)
			return n, err
		}
		w.line = append(w.line, part...)
		body = body[len(part):]
		if w.line[len(w.line)-1] == '\n' {
			if _, err := w.output.Write(w.line); err != nil {
				return 0, err
			}
			w.bytes += len(w.line)
			w.line = w.line[:0]
		}
	}
	return n, nil
}
