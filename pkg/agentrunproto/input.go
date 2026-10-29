package agentrunproto

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

// The input drive (ADR-20261027090010 §3) is a raw, read-only block device: one
// InputHeaderBytes header, then a checkout's tar.gz archive, zero-padded to
// a whole sector. Hosts write it without reading the archive; guest-init
// verifies it against the run request and unpacks it inside the VM.

const (
	// InputKeyPrefix starts every input drive's storage key. vmmd attaches
	// only keys under it as an input drive.
	InputKeyPrefix = "agents/inputs/"
	// InputMagic starts an input drive.
	InputMagic = "GRGLAIN1"
	// InputHeaderBytes is the header's size, one sector.
	InputHeaderBytes = 512
	inputSectorBytes = 512
	// InputDriveBytes is the input drive's fixed capacity. Every agent run VM
	// has one, zeros when the run has no checkout, so a VM cold-booted for a
	// preset snapshot and every VM restored from it agree on its size: a
	// restored guest keeps the capacity it probed at capture.
	InputDriveBytes int64 = InputHeaderBytes + 256<<20
)

var (
	// ErrInputDrive means the drive is not an input drive, or does not hold
	// the archive the request names.
	ErrInputDrive = errors.New("agentrunproto: input drive does not match the run")
	// ErrCheckoutTooLarge means the archive unpacks past the run's limits.
	ErrCheckoutTooLarge = errors.New("agentrunproto: checkout exceeds the run's limits")
)

// Checkout names the archive on the run's input drive and bounds what
// guest-init unpacks from it.
type Checkout struct {
	SHA256            string `json:"sha256"`
	Bytes             int64  `json:"bytes"`
	MaxExtractedBytes int64  `json:"max_extracted_bytes"`
	MaxEntries        int    `json:"max_entries"`
}

// Validate checks the request's checkout before anything reads the drive.
func (c Checkout) Validate() error {
	if raw, err := hex.DecodeString(c.SHA256); err != nil || len(raw) != sha256.Size || c.SHA256 != strings.ToLower(c.SHA256) {
		return fmt.Errorf("%w: checkout sha256 is not a lowercase hex digest", ErrInvalidRequest)
	}
	if c.Bytes < 1 || c.MaxExtractedBytes < 1 || c.MaxEntries < 1 {
		return fmt.Errorf("%w: checkout sizes must be positive", ErrInvalidRequest)
	}
	return nil
}

// InputImageKey is the storage key of an account's input image with the
// given archive digest.
func InputImageKey(accountID, digest string) string {
	return InputKeyPrefix + "images/" + accountID + "/" + digest + ".img"
}

// ValidInputKey reports whether key names one of the account's input images
// and nothing else, so a start frame can attach neither another artifact
// nor another account's checkout.
func ValidInputKey(key, accountID string) bool {
	rest, ok := strings.CutPrefix(key, InputKeyPrefix+"images/"+accountID+"/")
	digest, isImage := strings.CutSuffix(rest, ".img")
	raw, err := hex.DecodeString(digest)
	return ok && accountID != "" && !strings.Contains(accountID, "/") && isImage && err == nil && len(raw) == sha256.Size &&
		digest == strings.ToLower(digest)
}

// InputImageBytes is the size of the input drive for an archive of size
// bytes: the header plus the archive rounded up to a whole sector.
func InputImageBytes(size int64) int64 {
	return InputHeaderBytes + (size+inputSectorBytes-1)/inputSectorBytes*inputSectorBytes
}

// WriteInputImage writes the drive for an archive of exactly size bytes
// whose SHA-256 is sum. It copies the archive without interpreting it.
func WriteInputImage(w io.Writer, archive io.Reader, size int64, sum [sha256.Size]byte) error {
	if size < 1 {
		return errors.New("agentrunproto: input archive is empty")
	}
	header := make([]byte, InputHeaderBytes)
	copy(header, InputMagic)
	binary.BigEndian.PutUint64(header[8:16], uint64(size))
	copy(header[16:16+sha256.Size], sum[:])
	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("agentrunproto: write input header: %w", err)
	}
	n, err := io.CopyN(w, archive, size)
	if err != nil {
		return fmt.Errorf("agentrunproto: write input archive (%d of %d bytes): %w", n, size, err)
	}
	if pad := InputImageBytes(size) - InputHeaderBytes - size; pad > 0 {
		if _, err := w.Write(make([]byte, pad)); err != nil {
			return fmt.Errorf("agentrunproto: pad input archive: %w", err)
		}
	}
	return nil
}

// ReadInputHeader reads a drive's header. ok is false when the drive is not
// an input drive at all.
func ReadInputHeader(r io.ReaderAt) (size int64, sum [sha256.Size]byte, ok bool, err error) {
	header := make([]byte, InputHeaderBytes)
	if _, err := r.ReadAt(header, 0); err != nil {
		if errors.Is(err, io.EOF) {
			return 0, sum, false, nil
		}
		return 0, sum, false, fmt.Errorf("agentrunproto: read input header: %w", err)
	}
	if !bytes.Equal(header[:len(InputMagic)], []byte(InputMagic)) {
		return 0, sum, false, nil
	}
	size = int64(binary.BigEndian.Uint64(header[8:16]))
	copy(sum[:], header[16:16+sha256.Size])
	return size, sum, true, nil
}

// OpenInputArchive checks that drive holds the archive c names, hashing all
// of it, and returns a reader over the archive.
func OpenInputArchive(drive io.ReaderAt, c Checkout) (io.Reader, error) {
	size, sum, ok, err := ReadInputHeader(drive)
	if err != nil {
		return nil, err
	}
	if !ok || size != c.Bytes || hex.EncodeToString(sum[:]) != c.SHA256 {
		return nil, ErrInputDrive
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(drive, InputHeaderBytes, size)); err != nil {
		return nil, fmt.Errorf("agentrunproto: hash input archive: %w", err)
	}
	if hex.EncodeToString(h.Sum(nil)) != c.SHA256 {
		return nil, ErrInputDrive
	}
	return io.NewSectionReader(drive, InputHeaderBytes, size), nil
}

// ExtractCheckout unpacks a GitHub commit archive into dir: the archive's
// single top-level directory is dropped, writes stay inside dir (os.Root),
// and the entry count and unpacked bytes are bounded. Directories, regular
// files and symlinks are kept; anything else is skipped.
func ExtractCheckout(archive io.Reader, dir string, c Checkout) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("agentrunproto: create checkout dir: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("agentrunproto: open checkout dir: %w", err)
	}
	defer func() { _ = root.Close() }()
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("agentrunproto: checkout is not gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	var entries int
	var total int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("agentrunproto: read checkout: %w", err)
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if entries++; entries > c.MaxEntries {
			return fmt.Errorf("%w: more than %d entries", ErrCheckoutTooLarge, c.MaxEntries)
		}
		name, ok := checkoutPath(hdr.Name)
		if !ok {
			continue
		}
		if err := extractEntry(root, tr, hdr, name, &total, c.MaxExtractedBytes); err != nil {
			return err
		}
	}
}

// checkoutPath drops the archive's top-level directory and refuses names
// that are absolute or climb out.
func checkoutPath(name string) (string, bool) {
	name = strings.TrimPrefix(name, "./")
	top, rest, found := strings.Cut(name, "/")
	if !found || top == "" || top == ".." {
		return "", false
	}
	rest = strings.TrimSuffix(rest, "/")
	if rest == "" || path.IsAbs(rest) || !fs.ValidPath(rest) {
		return "", false
	}
	return rest, true
}

func extractEntry(root *os.Root, tr *tar.Reader, hdr *tar.Header, name string, total *int64, maxBytes int64) error {
	switch hdr.Typeflag {
	case tar.TypeDir:
		if err := root.MkdirAll(name, 0o755); err != nil {
			return fmt.Errorf("agentrunproto: checkout dir %s: %w", name, err)
		}
	case tar.TypeReg:
		if *total += hdr.Size; hdr.Size < 0 || *total > maxBytes {
			return fmt.Errorf("%w: more than %d bytes", ErrCheckoutTooLarge, maxBytes)
		}
		if err := root.MkdirAll(path.Dir(name), 0o755); err != nil {
			return fmt.Errorf("agentrunproto: checkout dir for %s: %w", name, err)
		}
		mode := fs.FileMode(0o644)
		if hdr.Mode&0o111 != 0 {
			mode = 0o755
		}
		f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return fmt.Errorf("agentrunproto: checkout file %s: %w", name, err)
		}
		_, copyErr := io.CopyN(f, tr, hdr.Size)
		if err := errors.Join(copyErr, f.Close()); err != nil {
			return fmt.Errorf("agentrunproto: checkout file %s: %w", name, err)
		}
	case tar.TypeSymlink:
		if err := root.MkdirAll(path.Dir(name), 0o755); err != nil {
			return fmt.Errorf("agentrunproto: checkout dir for %s: %w", name, err)
		}
		if err := root.Symlink(hdr.Linkname, name); err != nil {
			return fmt.Errorf("agentrunproto: checkout symlink %s: %w", name, err)
		}
	}
	return nil
}
