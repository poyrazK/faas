package crashcrypt

// The chunked format (v2, ADR-20261110210000 slice 3d-5) lets a sealed
// snapshot be opened in parallel and without touching its zero pages. v1
// (Encrypt) is one age stream over the whole zstd-compressed object: a wake
// decrypts and decompresses every byte of guest memory, zeros included, on
// one core, which took a 2 GB snapshot from a 1.15 s wake to about 7 s.
//
// v2 layout:
//
//	magic        "faas-sealed-v2\n"
//	key length   uvarint, then an age file holding the 32-byte data key
//	chunk size   uint32 big-endian
//	records      index uint64 BE | length uint32 BE | sealed chunk
//	end record   index 0xFFFFFFFFFFFFFFFF | length | sealed trailer
//
// A record holds one chunk of the plaintext at offset index*chunk size,
// zstd-compressed and sealed with ChaCha20-Poly1305 under the data key, the
// nonce being the index; chunks that are all zero are left out. Records come
// in increasing index order. The trailer seals the plaintext length, the
// record count and a SHA-256 over the record indices, so a reader detects a
// dropped, repeated, reordered or truncated record: without it, a missing
// chunk would silently read as zeros.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"

	"filippo.io/age"
	"github.com/klauspost/compress/zstd"
	"golang.org/x/crypto/chacha20poly1305"
)

// ChunkedMagic opens a v2 object.
const ChunkedMagic = "faas-sealed-v2\n"

// ageMagic opens a v1 object (an age file).
const ageMagic = "age-encryption.org/"

const (
	// ChunkSize is the plaintext span of one record.
	ChunkSize = 4 << 20
	// endIndex marks the trailer record.
	endIndex = ^uint64(0)
	// maxKeyFile bounds the age file around the data key.
	maxKeyFile = 4 << 10
	// maxChunkSize bounds a header's chunk size.
	maxChunkSize = 64 << 20
	trailerLen   = 8 + 8 + sha256.Size
	// maxIndex bounds a record index so index*chunk size cannot overflow
	// (2^40 chunks of 4 MiB is far past any object).
	maxIndex   = 1 << 40
	chunkAAD   = "faas-sealed-v2 chunk"
	trailerAAD = "faas-sealed-v2 trailer"
)

// ErrCorrupt means a v2 object failed authentication or does not have the
// shape its trailer declares.
var ErrCorrupt = errors.New("crashcrypt: sealed object is corrupt or was altered")

var (
	zstdEncoder, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
	zstdDecoder, _ = zstd.NewReader(nil, zstd.WithDecoderConcurrency(0), zstd.WithDecoderMaxMemory(maxChunkSize))
)

func nonce(index uint64) []byte {
	n := make([]byte, chacha20poly1305.NonceSize)
	binary.BigEndian.PutUint64(n[4:], index)
	return n
}

func workers() int {
	n := runtime.GOMAXPROCS(0)
	if n > 8 {
		n = 8
	}
	return n
}

// EncryptChunked writes src to dst in the v2 format, encrypted to
// recipient. Chunks are compressed and sealed on up to eight cores and
// written in order.
func EncryptChunked(dst io.Writer, src io.Reader, recipient age.Recipient) error {
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("crashcrypt: data key: %w", err)
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return err
	}
	var wrapped bytes.Buffer
	aw, err := age.Encrypt(&wrapped, recipient)
	if err != nil {
		return err
	}
	if _, err := aw.Write(key); err != nil {
		return err
	}
	if err := aw.Close(); err != nil {
		return err
	}
	bw := bufio.NewWriterSize(dst, 1<<20)
	header := make([]byte, 0, len(ChunkedMagic)+binary.MaxVarintLen64+wrapped.Len()+4)
	header = append(header, ChunkedMagic...)
	header = binary.AppendUvarint(header, uint64(wrapped.Len()))
	header = append(header, wrapped.Bytes()...)
	header = binary.BigEndian.AppendUint32(header, ChunkSize)
	if _, err := bw.Write(header); err != nil {
		return err
	}

	type job struct {
		index uint64
		plain []byte
		out   chan []byte
	}
	jobs := make(chan job)
	pending := make(chan job, workers()*2)
	var wg sync.WaitGroup
	for range workers() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				compressed := zstdEncoder.EncodeAll(j.plain, make([]byte, 0, len(j.plain)/4))
				j.out <- aead.Seal(nil, nonce(j.index), compressed, chunkAADFor(j.index))
			}
		}()
	}
	indexHash := sha256.New()
	var count uint64
	writeErr := make(chan error, 1)
	go func() {
		var err error
		for j := range pending {
			sealed := <-j.out
			if err != nil {
				continue
			}
			var rec [12]byte
			binary.BigEndian.PutUint64(rec[:8], j.index)
			binary.BigEndian.PutUint32(rec[8:], uint32(len(sealed)))
			if _, err = bw.Write(rec[:]); err == nil {
				_, err = bw.Write(sealed)
			}
			_, _ = indexHash.Write(rec[:8])
			count++
		}
		writeErr <- err
	}()

	var size uint64
	var readErr error
	for index := uint64(0); ; index++ {
		buf := make([]byte, ChunkSize)
		n, err := io.ReadFull(src, buf)
		if n > 0 {
			size += uint64(n)
			if !isZero(buf[:n]) {
				j := job{index: index, plain: buf[:n], out: make(chan []byte, 1)}
				pending <- j
				jobs <- j
			}
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			break
		}
		if err != nil {
			readErr = err
			break
		}
	}
	close(jobs)
	wg.Wait()
	close(pending)
	if err := <-writeErr; err != nil {
		return err
	}
	if readErr != nil {
		return readErr
	}
	trailer := make([]byte, 0, trailerLen)
	trailer = binary.BigEndian.AppendUint64(trailer, size)
	trailer = binary.BigEndian.AppendUint64(trailer, count)
	trailer = indexHash.Sum(trailer)
	sealed := aead.Seal(nil, nonce(endIndex), trailer, []byte(trailerAAD))
	var rec [12]byte
	binary.BigEndian.PutUint64(rec[:8], endIndex)
	binary.BigEndian.PutUint32(rec[8:], uint32(len(sealed)))
	if _, err := bw.Write(rec[:]); err != nil {
		return err
	}
	if _, err := bw.Write(sealed); err != nil {
		return err
	}
	return bw.Flush()
}

func chunkAADFor(index uint64) []byte {
	return binary.BigEndian.AppendUint64([]byte(chunkAAD), index)
}

// chunkedReader parses a v2 object after its magic.
type chunkedReader struct {
	src  *bufio.Reader
	aead interface {
		Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
		Overhead() int
	}
	chunkSize uint64
	next      uint64 // lowest index the next record may carry
	count     uint64
	indexHash []byte
	hash      interface {
		Write([]byte) (int, error)
		Sum([]byte) []byte
	}
}

func openChunked(src *bufio.Reader, identity age.Identity) (*chunkedReader, error) {
	keyLen, err := binary.ReadUvarint(src)
	if err != nil || keyLen == 0 || keyLen > maxKeyFile {
		return nil, ErrCorrupt
	}
	wrapped := make([]byte, keyLen)
	if _, err := io.ReadFull(src, wrapped); err != nil {
		return nil, ErrCorrupt
	}
	ar, err := age.Decrypt(bytes.NewReader(wrapped), identity)
	if err != nil {
		return nil, fmt.Errorf("crashcrypt: open data key: %w", err)
	}
	key, err := io.ReadAll(io.LimitReader(ar, chacha20poly1305.KeySize+1))
	if err != nil || len(key) != chacha20poly1305.KeySize {
		return nil, ErrCorrupt
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	var cs [4]byte
	if _, err := io.ReadFull(src, cs[:]); err != nil {
		return nil, ErrCorrupt
	}
	chunkSize := uint64(binary.BigEndian.Uint32(cs[:]))
	if chunkSize == 0 || chunkSize > maxChunkSize {
		return nil, ErrCorrupt
	}
	return &chunkedReader{src: src, aead: aead, chunkSize: chunkSize, hash: sha256.New()}, nil
}

// record returns the next chunk's index and plaintext, or done with the
// verified plaintext length after the trailer.
func (r *chunkedReader) record() (index uint64, sealed []byte, end bool, size uint64, err error) {
	var rec [12]byte
	if _, err := io.ReadFull(r.src, rec[:]); err != nil {
		return 0, nil, false, 0, ErrCorrupt
	}
	index = binary.BigEndian.Uint64(rec[:8])
	length := uint64(binary.BigEndian.Uint32(rec[8:]))
	if length > r.chunkSize+r.chunkSize/8+1024 {
		return 0, nil, false, 0, ErrCorrupt
	}
	sealed = make([]byte, length)
	if _, err := io.ReadFull(r.src, sealed); err != nil {
		return 0, nil, false, 0, ErrCorrupt
	}
	if index == endIndex {
		trailer, err := r.aead.Open(nil, nonce(endIndex), sealed, []byte(trailerAAD))
		if err != nil || len(trailer) != trailerLen {
			return 0, nil, false, 0, ErrCorrupt
		}
		size = binary.BigEndian.Uint64(trailer[:8])
		if binary.BigEndian.Uint64(trailer[8:16]) != r.count || !bytes.Equal(trailer[16:], r.hash.Sum(nil)) {
			return 0, nil, false, 0, ErrCorrupt
		}
		if r.count > 0 && (r.next-1)*r.chunkSize >= size {
			return 0, nil, false, 0, ErrCorrupt
		}
		if _, err := r.src.ReadByte(); err != io.EOF {
			return 0, nil, false, 0, ErrCorrupt
		}
		return 0, nil, true, size, nil
	}
	if index < r.next || index > maxIndex {
		return 0, nil, false, 0, ErrCorrupt
	}
	r.next = index + 1
	r.count++
	_, _ = r.hash.Write(rec[:8])
	return index, sealed, false, 0, nil
}

func (r *chunkedReader) open(index uint64, sealed []byte) ([]byte, error) {
	compressed, err := r.aead.Open(nil, nonce(index), sealed, chunkAADFor(index))
	if err != nil {
		return nil, ErrCorrupt
	}
	plain, err := zstdDecoder.DecodeAll(compressed, make([]byte, 0, r.chunkSize))
	if err != nil || uint64(len(plain)) > r.chunkSize {
		return nil, ErrCorrupt
	}
	return plain, nil
}

// DecryptToFile writes src's plaintext into dst at offset 0 and sets dst's
// length, decrypting chunks in parallel and leaving zero chunks as holes. A
// v1 object streams through Decrypt and CopySparse. On error dst holds a
// partial plaintext the caller must remove.
func DecryptToFile(ctx context.Context, dst *os.File, src io.Reader, identity age.Identity) (int64, error) {
	br := bufio.NewReaderSize(src, 1<<20)
	head, err := br.Peek(len(ChunkedMagic))
	if err != nil || string(head) != ChunkedMagic {
		plain, err := decryptV1(br, identity)
		if err != nil {
			return 0, err
		}
		defer func() { _ = plain.Close() }()
		return CopySparse(dst, plain)
	}
	if _, err := br.Discard(len(ChunkedMagic)); err != nil {
		return 0, err
	}
	r, err := openChunked(br, identity)
	if err != nil {
		return 0, err
	}
	type job struct {
		index  uint64
		sealed []byte
	}
	jobs := make(chan job, workers()*2)
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}
	for range workers() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				plain, err := r.open(j.index, j.sealed)
				if err != nil {
					fail(err)
					continue
				}
				if err := writeSparseAt(dst, plain, int64(j.index*r.chunkSize)); err != nil {
					fail(err)
				}
			}
		}()
	}
	var size uint64
	for {
		if err := ctx.Err(); err != nil {
			fail(err)
			break
		}
		index, sealed, end, total, err := r.record()
		if err != nil {
			fail(err)
			break
		}
		if end {
			size = total
			break
		}
		jobs <- job{index: index, sealed: sealed}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return 0, firstErr
	}
	if err := dst.Truncate(int64(size)); err != nil {
		return 0, err
	}
	return int64(size), nil
}

// writeSparseAt writes b at off, skipping all-zero sparseBlock runs so they
// stay holes, and coalescing the rest into as few writes as possible.
func writeSparseAt(dst *os.File, b []byte, off int64) error {
	start := -1
	for i := 0; i < len(b); i += sparseBlock {
		end := min(i+sparseBlock, len(b))
		if isZero(b[i:end]) {
			if start >= 0 {
				if _, err := dst.WriteAt(b[start:i], off+int64(start)); err != nil {
					return err
				}
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		if _, err := dst.WriteAt(b[start:], off+int64(start)); err != nil {
			return err
		}
	}
	return nil
}

// decryptV1 opens a v1 object (age stream of zstd).
func decryptV1(src io.Reader, identity age.Identity) (io.ReadCloser, error) {
	ar, err := age.Decrypt(src, identity)
	if err != nil {
		return nil, err
	}
	zr, err := zstd.NewReader(ar, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	return zr.IOReadCloser(), nil
}

// chunkedStream reads a v2 object in order as a plain stream, zero-filling
// the chunks left out; it serves Backend.Get, whose callers read vmstate
// and other small objects.
type chunkedStream struct {
	r       *chunkedReader
	offset  uint64 // plaintext bytes emitted
	buf     []byte // current chunk's remaining plaintext
	pending struct {
		ok     bool
		index  uint64
		sealed []byte
	}
	size uint64 // known once the trailer is read
	done bool
}

func (s *chunkedStream) Read(p []byte) (int, error) {
	for len(s.buf) == 0 {
		if s.done && s.offset >= s.size {
			return 0, io.EOF
		}
		if !s.pending.ok && !s.done {
			index, sealed, end, size, err := s.r.record()
			if err != nil {
				return 0, err
			}
			if end {
				s.done, s.size = true, size
			} else {
				s.pending.ok, s.pending.index, s.pending.sealed = true, index, sealed
			}
		}
		chunkStart := s.offset
		if s.done && !s.pending.ok {
			if chunkStart > s.size {
				return 0, ErrCorrupt
			}
			if chunkStart == s.size {
				return 0, io.EOF
			}
		}
		if s.pending.ok && s.pending.index*s.r.chunkSize == chunkStart {
			plain, err := s.r.open(s.pending.index, s.pending.sealed)
			if err != nil {
				return 0, err
			}
			s.pending.ok = false
			s.buf = plain
			continue
		}
		// A chunk left out (all zero) up to the next record or the end.
		limit := chunkStart + s.r.chunkSize - chunkStart%s.r.chunkSize
		if s.pending.ok {
			limit = min(limit, s.pending.index*s.r.chunkSize)
		} else if s.done {
			limit = min(limit, s.size)
		}
		if limit <= chunkStart {
			return 0, ErrCorrupt
		}
		s.buf = make([]byte, limit-chunkStart)
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	s.offset += uint64(n)
	return n, nil
}

// openStream returns src's plaintext as a stream for either format.
func openStream(src io.Reader, identity age.Identity) (io.ReadCloser, error) {
	br := bufio.NewReaderSize(src, 1<<20)
	head, err := br.Peek(len(ChunkedMagic))
	if err != nil || string(head) != ChunkedMagic {
		return decryptV1(br, identity)
	}
	if _, err := br.Discard(len(ChunkedMagic)); err != nil {
		return nil, err
	}
	r, err := openChunked(br, identity)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(&chunkedStream{r: r}), nil
}
