package crashcrypt

// adr: 20261110210000

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"
)

// memoryImage is a guest-memory-like plaintext: mostly zero, with random
// pages scattered through it and a short last chunk.
func memoryImage(t testing.TB, size, dataEvery int) []byte {
	t.Helper()
	b := make([]byte, size)
	for off := 0; off < size; off += dataEvery {
		end := min(off+64<<10, size)
		if _, err := rand.Read(b[off:end]); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

func sealChunked(t testing.TB, plain []byte, id *age.X25519Identity) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := EncryptChunked(&out, bytes.NewReader(plain), id.Recipient()); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func decryptFile(t testing.TB, sealed []byte, id *age.X25519Identity) ([]byte, error) {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "plain"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	n, err := DecryptToFile(context.Background(), f, bytes.NewReader(sealed), id)
	if err != nil {
		return nil, err
	}
	got, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(got)) != n {
		t.Fatalf("file length %d, reported %d", len(got), n)
	}
	return got, nil
}

func TestChunkedRoundTrip(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	for name, plain := range map[string][]byte{
		"empty":           {},
		"all zero":        make([]byte, 3*ChunkSize+5),
		"short":           []byte("vmstate"),
		"sparse memory":   memoryImage(t, 5*ChunkSize+123, 3*ChunkSize/2),
		"trailing zeros":  append(memoryImage(t, ChunkSize, ChunkSize/2), make([]byte, 2*ChunkSize)...),
		"one exact chunk": memoryImage(t, ChunkSize, 1<<20),
	} {
		t.Run(name, func(t *testing.T) {
			sealed := sealChunked(t, plain, id)
			got, err := decryptFile(t, sealed, id)
			if err != nil || !bytes.Equal(got, plain) {
				t.Fatalf("DecryptToFile: err=%v equal=%v (len %d vs %d)", err, bytes.Equal(got, plain), len(got), len(plain))
			}
			rc, err := Decrypt(bytes.NewReader(sealed), id)
			if err != nil {
				t.Fatal(err)
			}
			streamed, err := io.ReadAll(rc)
			if err != nil || !bytes.Equal(streamed, plain) {
				t.Fatalf("Decrypt stream: err=%v equal=%v (len %d vs %d)", err, bytes.Equal(streamed, plain), len(streamed), len(plain))
			}
		})
	}
}

// Zero chunks never reach the object: a mostly-zero image seals to roughly
// its non-zero bytes.
func TestChunkedSkipsZeroChunks(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	plain := make([]byte, 64*ChunkSize)
	if _, err := rand.Read(plain[10*ChunkSize : 11*ChunkSize]); err != nil {
		t.Fatal(err)
	}
	if sealed := sealChunked(t, plain, id); len(sealed) > 2*ChunkSize {
		t.Fatalf("sealed %d bytes for one non-zero chunk", len(sealed))
	}
}

// Every alteration a store holder could make is refused, never read as
// zeros or as a shorter object.
func TestChunkedRefusesTampering(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	other, _ := age.GenerateX25519Identity()
	plain := memoryImage(t, 4*ChunkSize, ChunkSize)
	sealed := sealChunked(t, plain, id)
	// Locate the first record after the header.
	header := len(ChunkedMagic)
	keyLen, n := binary.Uvarint(sealed[header:])
	first := header + n + int(keyLen) + 4
	firstLen := int(binary.BigEndian.Uint32(sealed[first+8:]))
	second := first + 12 + firstLen

	flip := append([]byte(nil), sealed...)
	flip[first+12+firstLen/2] ^= 1
	dropped := append(append([]byte(nil), sealed[:first]...), sealed[second:]...)
	renumbered := append([]byte(nil), sealed...)
	binary.BigEndian.PutUint64(renumbered[first:], 2)
	for name, tc := range map[string]struct {
		data []byte
		id   *age.X25519Identity
	}{
		"flipped byte":      {flip, id},
		"dropped record":    {dropped, id},
		"renumbered record": {renumbered, id},
		"truncated":         {sealed[:len(sealed)-10], id},
		"no trailer":        {sealed[:second], id},
		"extra bytes":       {append(append([]byte(nil), sealed...), 0), id},
		"wrong identity":    {sealed, other},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decryptFile(t, tc.data, tc.id); err == nil {
				t.Fatal("DecryptToFile accepted an altered object")
			}
			rc, err := Decrypt(bytes.NewReader(tc.data), tc.id)
			if err == nil {
				_, err = io.ReadAll(rc)
			}
			if err == nil {
				t.Fatal("Decrypt accepted an altered object")
			}
			if name != "wrong identity" && !errors.Is(err, ErrCorrupt) {
				t.Fatalf("err = %v, want ErrCorrupt", err)
			}
		})
	}
}

// v1 objects stay readable through the parallel path.
func TestDecryptToFileReadsV1(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	plain := memoryImage(t, 2*ChunkSize+7, ChunkSize)
	var v1 bytes.Buffer
	if err := Encrypt(&v1, bytes.NewReader(plain), id.Recipient()); err != nil {
		t.Fatal(err)
	}
	got, err := decryptFile(t, v1.Bytes(), id)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("v1 via DecryptToFile: err=%v equal=%v", err, bytes.Equal(got, plain))
	}
}

// TestChunkedFasterThanV1 records both formats' open time for a 512 MiB
// image with 1/16 of it non-zero (CRASHCRYPT_BENCH=1; it writes 1 GB).
func TestChunkedFasterThanV1(t *testing.T) {
	if os.Getenv("CRASHCRYPT_BENCH") != "1" {
		t.Skip("set CRASHCRYPT_BENCH=1")
	}
	id, _ := age.GenerateX25519Identity()
	plain := memoryImage(t, 512<<20, 1<<20)
	var v1 bytes.Buffer
	if err := Encrypt(&v1, bytes.NewReader(plain), id.Recipient()); err != nil {
		t.Fatal(err)
	}
	v2 := sealChunked(t, plain, id)
	for name, data := range map[string][]byte{"v1": v1.Bytes(), "v2": v2} {
		start := time.Now()
		if _, err := decryptFile(t, data, id); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: sealed %d MiB, open %v", name, len(data)>>20, time.Since(start))
	}
}
