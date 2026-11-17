package main

import (
	"bytes"
	"io"
	"sort"
	"sync"
	"sync/atomic"
)

// secretRedactMinBytes is the shortest sealed secret value that is masked in
// the workload's output. Shorter values ("true", "1", "8080") are common
// words and numbers; masking them would garble ordinary log lines.
const secretRedactMinBytes = 8

// secretRedactor masks sealed secret values in the main workload's output
// before it reaches the serial console, and with it the logs API, drains and
// log search (spec §17 G2: values are redacted from app logs). On production
// (2026-11-09) an app that printed its own sealed value showed it verbatim in
// `gregale logs` (ADR-20261110090000).
//
// The set only grows: a runtime secret reload adds the new values and keeps
// the old ones, which a still-running process may print.
type secretRedactor struct {
	mu  sync.Mutex
	set atomic.Pointer[secretSet]
}

type secretSet struct {
	byKey   map[string]map[string]struct{}
	byFirst map[byte][]secretValue
}

type secretValue struct {
	value  []byte
	marker []byte
}

func newSecretRedactor(secrets map[string]string) *secretRedactor {
	r := &secretRedactor{}
	r.update(secrets)
	return r
}

// update adds secrets' values to the set.
func (r *secretRedactor) update(secrets map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	byKey := map[string]map[string]struct{}{}
	if old := r.set.Load(); old != nil {
		for key, values := range old.byKey {
			byKey[key] = map[string]struct{}{}
			for v := range values {
				byKey[key][v] = struct{}{}
			}
		}
	}
	for key, value := range secrets {
		if len(value) < secretRedactMinBytes {
			continue
		}
		if byKey[key] == nil {
			byKey[key] = map[string]struct{}{}
		}
		byKey[key][value] = struct{}{}
	}
	seen := map[string]bool{}
	byFirst := map[byte][]secretValue{}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for value := range byKey[key] {
			if seen[value] {
				continue
			}
			seen[value] = true
			byFirst[value[0]] = append(byFirst[value[0]], secretValue{value: []byte(value), marker: []byte("[REDACTED:" + key + "]")})
		}
	}
	// Longest first, so a value that contains another is masked whole.
	for _, values := range byFirst {
		sort.Slice(values, func(i, j int) bool {
			if len(values[i].value) != len(values[j].value) {
				return len(values[i].value) > len(values[j].value)
			}
			return bytes.Compare(values[i].value, values[j].value) < 0
		})
	}
	r.set.Store(&secretSet{byKey: byKey, byFirst: byFirst})
}

// scan masks every complete secret value in buf. Unless final, it stops at a
// tail that could still grow into a value and returns it as held, so a value
// split across two writes is masked too.
func (s *secretSet) scan(buf []byte, final bool) (out, held []byte) {
	if s == nil || len(s.byFirst) == 0 {
		return buf, nil
	}
	out = make([]byte, 0, len(buf))
	for i := 0; i < len(buf); {
		values := s.byFirst[buf[i]]
		if len(values) == 0 {
			out = append(out, buf[i])
			i++
			continue
		}
		rest := buf[i:]
		if !final {
			for _, v := range values {
				if len(rest) < len(v.value) && bytes.HasPrefix(v.value, rest) {
					return out, rest
				}
			}
		}
		matched := false
		for _, v := range values {
			if bytes.HasPrefix(rest, v.value) {
				out = append(out, v.marker...)
				i += len(v.value)
				matched = true
				break
			}
		}
		if !matched {
			out = append(out, buf[i])
			i++
		}
	}
	return out, nil
}

// writer returns a writer that masks this redactor's values in what it
// forwards to dst.
func (r *secretRedactor) writer(dst io.Writer) *redactingWriter {
	return &redactingWriter{redactor: r, dst: dst}
}

// redactingWriter forwards writes to dst with secret values masked. It holds
// back at most one value's length minus one byte, until the next write or
// Flush.
type redactingWriter struct {
	mu       sync.Mutex
	redactor *secretRedactor
	dst      io.Writer
	pending  []byte
}

func (w *redactingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p...)
	if err := w.emitLocked(false); err != nil {
		return len(p), err
	}
	return len(p), nil
}

// Flush forwards the held tail; the workload's output pipe calls it at EOF.
func (w *redactingWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.emitLocked(true)
}

func (w *redactingWriter) emitLocked(final bool) error {
	out, held := w.redactor.set.Load().scan(w.pending, final)
	w.pending = append(w.pending[:0:0], held...)
	if len(out) == 0 {
		return nil
	}
	_, err := w.dst.Write(out)
	return err
}
