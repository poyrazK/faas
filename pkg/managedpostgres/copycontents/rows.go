package copycontents

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"hash"

	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

// COPY text has unambiguous escaped fields and one physical newline per row.
// We hash its exact canonical output, never hold a whole row, and retain every
// duplicate in the sorted digest multiset. NULL, empty and literal \\N differ.
type rowWriter struct {
	sorter                   *digestSorter
	hash                     hash.Hash
	fields, tabs             int
	escaped                  bool
	rowBytes, bytes, maximum int64
	key                      [32]byte
	domain                   []byte
	err                      error
}

func newRowWriter(sorter *digestSorter, fields int, maximum int64, key [32]byte, domain []byte) *rowWriter {
	w := &rowWriter{sorter: sorter, fields: fields, maximum: maximum, key: key, domain: domain}
	w.reset()
	return w
}
func (w *rowWriter) reset() {
	w.hash = keyedHash(w.key, "gregale-copy-contents-row-v1", w.domain)
	var fields [8]byte
	binary.BigEndian.PutUint64(fields[:], uint64(w.fields))
	_, _ = w.hash.Write(fields[:])
	w.tabs = 0
	w.rowBytes = 0
	w.escaped = false
}
func (w *rowWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if int64(len(p)) > w.maximum-w.bytes {
		w.err = pgerrors.ErrQuotaExceeded
		return 0, w.err
	}
	if e := w.sorter.ctx.Err(); e != nil {
		w.err = e
		return 0, e
	}
	w.bytes += int64(len(p))
	start := 0
	for i, c := range p {
		if w.escaped {
			if !bytes.ContainsRune([]byte("\\bfnrtvN"), rune(c)) {
				w.err = pgerrors.ErrConflict
				return i, w.err
			}
			w.escaped = false
			continue
		}
		switch c {
		case '\\':
			w.escaped = true
		case '\t':
			w.tabs++
		case '\r':
			w.err = pgerrors.ErrConflict
			return i, w.err
		case '\n':
			_, _ = w.hash.Write(p[start : i+1])
			w.rowBytes += int64(i - start)
			if (w.fields == 0 && w.rowBytes != 0) || (w.fields > 0 && w.tabs != w.fields-1) {
				w.err = pgerrors.ErrConflict
				return i, w.err
			}
			var d [sha256.Size]byte
			copy(d[:], w.hash.Sum(nil))
			if e := w.sorter.Add(d); e != nil {
				w.err = e
				return i, e
			}
			w.reset()
			start = i + 1
		}
	}
	if start < len(p) {
		_, _ = w.hash.Write(p[start:])
		w.rowBytes += int64(len(p) - start)
	}
	return len(p), nil
}
func (w *rowWriter) Finish() (string, error) {
	if w.err != nil {
		return "", w.err
	}
	if w.rowBytes != 0 || w.escaped || w.tabs != 0 {
		return "", pgerrors.ErrConflict
	}
	return w.sorter.Finish()
}
