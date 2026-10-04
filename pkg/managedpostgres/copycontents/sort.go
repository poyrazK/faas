package copycontents

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

type digestRun struct {
	file  *os.File
	bytes int64
}
type digestSorter struct {
	ctx                 context.Context
	dir                 string
	memory              [][sha256.Size]byte
	capacity            int
	levels              [api.PostgresCopyContentsSortLevelsMax]*digestRun
	disk, maxDisk, rows int64
	key                 [32]byte
	domain              []byte
}

func newDigestSorter(ctx context.Context, parent string, memory int, disk int64, key [32]byte, domain []byte) (*digestSorter, error) {
	if memory < sha256.Size || memory > api.PostgresCopyContentsSortMemoryMax || disk < sha256.Size || disk > api.PostgresCopyContentsSortDiskMax {
		return nil, pgerrors.ErrInvalid
	}
	dir, e := os.MkdirTemp(parent, "gregale-contents-")
	if e != nil {
		return nil, pgerrors.ErrUnavailable
	}
	return &digestSorter{ctx: ctx, dir: dir, memory: make([][sha256.Size]byte, 0, memory/sha256.Size), capacity: memory / sha256.Size, maxDisk: disk, key: key, domain: domain}, nil
}
func (s *digestSorter) Close() {
	for i, r := range s.levels {
		if r != nil {
			s.drop(r)
			s.levels[i] = nil
		}
	}
	_ = os.Remove(s.dir)
}
func (s *digestSorter) drop(r *digestRun) { _ = r.file.Close(); s.disk -= r.bytes }
func (s *digestSorter) file() (*digestRun, error) {
	f, e := os.CreateTemp(s.dir, "run-")
	if e != nil {
		return nil, pgerrors.ErrUnavailable
	}
	if e = os.Remove(f.Name()); e != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, pgerrors.ErrUnavailable
	}
	return &digestRun{file: f}, nil
}
func (s *digestSorter) write(r *digestRun, w *bufio.Writer, d []byte) error {
	if e := s.ctx.Err(); e != nil {
		return e
	}
	if int64(len(d)) > s.maxDisk-s.disk {
		return pgerrors.ErrQuotaExceeded
	}
	if _, e := w.Write(d); e != nil {
		return pgerrors.ErrUnavailable
	}
	s.disk += int64(len(d))
	r.bytes += int64(len(d))
	return nil
}
func (s *digestSorter) Add(d [sha256.Size]byte) error {
	if e := s.ctx.Err(); e != nil {
		return e
	}
	s.memory = append(s.memory, d)
	s.rows++
	if len(s.memory) == s.capacity {
		return s.flush()
	}
	return nil
}
func (s *digestSorter) flush() error {
	if len(s.memory) == 0 {
		return nil
	}
	sort.Slice(s.memory, func(i, j int) bool { return bytes.Compare(s.memory[i][:], s.memory[j][:]) < 0 })
	run, e := s.file()
	if e != nil {
		return e
	}
	keep := false
	defer func() {
		if !keep {
			s.drop(run)
		}
	}()
	writer := bufio.NewWriter(run.file)
	for _, d := range s.memory {
		if e = s.write(run, writer, d[:]); e != nil {
			return e
		}
	}
	if e = writer.Flush(); e != nil {
		return pgerrors.ErrUnavailable
	}
	s.memory = s.memory[:0]
	for i := range s.levels {
		if s.levels[i] == nil {
			s.levels[i] = run
			keep = true
			return nil
		}
		next, e := s.merge(s.levels[i], run)
		if e != nil {
			return e
		}
		s.drop(s.levels[i])
		s.levels[i] = nil
		s.drop(run)
		run = next
	}
	return pgerrors.ErrQuotaExceeded
}
func (s *digestSorter) merge(a, b *digestRun) (result *digestRun, err error) {
	if _, e := a.file.Seek(0, io.SeekStart); e != nil {
		return nil, pgerrors.ErrUnavailable
	}
	if _, e := b.file.Seek(0, io.SeekStart); e != nil {
		return nil, pgerrors.ErrUnavailable
	}
	result, err = s.file()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			s.drop(result)
			result = nil
		}
	}()
	ar, br := bufio.NewReader(a.file), bufio.NewReader(b.file)
	writer := bufio.NewWriter(result.file)
	var ad, bd [sha256.Size]byte
	ae, be := readDigest(ar, &ad), readDigest(br, &bd)
	for ae == nil || be == nil {
		if ae != nil && !errors.Is(ae, io.EOF) {
			return result, pgerrors.ErrConflict
		}
		if be != nil && !errors.Is(be, io.EOF) {
			return result, pgerrors.ErrConflict
		}
		if be != nil || (ae == nil && bytes.Compare(ad[:], bd[:]) <= 0) {
			if err = s.write(result, writer, ad[:]); err != nil {
				return result, err
			}
			ae = readDigest(ar, &ad)
		} else {
			if err = s.write(result, writer, bd[:]); err != nil {
				return result, err
			}
			be = readDigest(br, &bd)
		}
	}
	if !errors.Is(ae, io.EOF) || !errors.Is(be, io.EOF) {
		return result, pgerrors.ErrConflict
	}
	if err = writer.Flush(); err != nil {
		return result, pgerrors.ErrUnavailable
	}
	return result, nil
}
func readDigest(r io.Reader, d *[sha256.Size]byte) error { _, e := io.ReadFull(r, d[:]); return e }
func (s *digestSorter) Finish() (string, error) {
	if e := s.flush(); e != nil {
		return "", e
	}
	var result *digestRun
	for i, r := range s.levels {
		if r == nil {
			continue
		}
		s.levels[i] = nil
		if result == nil {
			result = r
			continue
		}
		merged, e := s.merge(result, r)
		if e != nil {
			s.drop(result)
			s.drop(r)
			return "", e
		}
		s.drop(result)
		s.drop(r)
		result = merged
	}
	h := keyedHash(s.key, "gregale-copy-contents-sorted-rows-v1", s.domain)
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(s.rows))
	_, _ = h.Write(count[:])
	if result != nil {
		defer s.drop(result)
		if _, e := result.file.Seek(0, io.SeekStart); e != nil {
			return "", pgerrors.ErrUnavailable
		}
		reader := bufio.NewReader(result.file)
		var d [sha256.Size]byte
		var seen int64
		for {
			if e := s.ctx.Err(); e != nil {
				return "", e
			}
			e := readDigest(reader, &d)
			if errors.Is(e, io.EOF) {
				break
			}
			if e != nil {
				return "", pgerrors.ErrConflict
			}
			_, _ = h.Write(d[:])
			seen++
		}
		if seen != s.rows {
			return "", pgerrors.ErrConflict
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func keyedHash(key [32]byte, namespace string, domain []byte) hash.Hash {
	h := hmac.New(sha256.New, key[:])
	_, _ = h.Write([]byte(namespace))
	_, _ = h.Write([]byte{0})
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(domain)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(domain)
	return h
}
