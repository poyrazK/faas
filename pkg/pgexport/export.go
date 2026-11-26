// Package pgexport captures and streams a standard PostgreSQL custom dump.
package pgexport

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/onebox-faas/faas/pkg/pgextension"
)

const (
	MaxBytes = 5 << 30
	Timeout  = 30 * time.Minute
	Chunk    = 64 << 10
)

var (
	ErrIncomplete = errors.New("PostgreSQL export did not complete")
	ErrTooLarge   = errors.New("PostgreSQL export exceeds the size limit")
	databaseName  = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
	snapshotID    = regexp.MustCompile(`^[0-9A-Fa-f-]{1,64}$`)
)

func ValidDatabase(database string) bool {
	return databaseName.MatchString(database) && database != "postgres" && database != "template0" && database != "template1"
}

type Source struct {
	Major         int                     `json:"postgres_major"`
	DatabaseBytes int64                   `json:"database_bytes,omitempty"`
	VectorSchema  string                  `json:"vector_schema,omitempty"`
	CapturedAt    time.Time               `json:"captured_at"`
	Extensions    []pgextension.Installed `json:"extensions"`
}

func (s Source) Valid() bool {
	if s.Major < 15 || s.Major > 18 || s.CapturedAt.IsZero() || s.Extensions == nil || len(s.Extensions) > pgextension.MaxEntries {
		return false
	}
	for _, extension := range s.Extensions {
		if !extension.Valid() {
			return false
		}
	}
	return true
}

func DecodeSource(raw []byte) (Source, error) {
	var source Source
	if len(raw) > Chunk || json.Unmarshal(raw, &source) != nil || !source.Valid() {
		return Source{}, ErrIncomplete
	}
	return source, nil
}

const captureSQL = `BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL search_path = pg_catalog;
SELECT json_build_object(
  'snapshot', pg_catalog.pg_export_snapshot(),
  'postgres_major', pg_catalog.current_setting('server_version_num')::int / 10000,
  'captured_at', CURRENT_TIMESTAMP,
  'database_bytes', pg_catalog.pg_database_size(pg_catalog.current_database()),
  'vector_schema', coalesce((SELECT n.nspname::text FROM pg_catalog.pg_extension e
       JOIN pg_catalog.pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='vector'), ''),
  'extensions', (` + pgextension.InstalledSQL + `)
);`

// Capture holds a read-only snapshot while pg_dump reads it. The launcher
// runs both fixed tools as the guest's local PostgreSQL OS user.
func Capture(ctx context.Context, bin, socket, database string, launch func(*exec.Cmd) *exec.Cmd, dst io.Writer) (Source, error) {
	if !ValidDatabase(database) {
		return Source{}, ErrIncomplete
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	connection := []string{"-h", socket, "-U", "postgres", "-d", database}
	psql := launch(exec.CommandContext(ctx, filepath.Join(bin, "psql"), append(connection, "-XAtqw", "-v", "ON_ERROR_STOP=1", "-f", "-")...))
	input, err := psql.StdinPipe()
	if err != nil {
		return Source{}, err
	}
	output, err := psql.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return Source{}, err
	}
	if err := psql.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		return Source{}, err
	}
	defer func() {
		cancel()
		_ = input.Close()
		_ = psql.Wait()
	}()
	if _, err := io.WriteString(input, captureSQL+"\n"); err != nil {
		return Source{}, err
	}
	line, err := bufio.NewReader(io.LimitReader(output, Chunk)).ReadBytes('\n')
	var capture struct {
		Source
		Snapshot string `json:"snapshot"`
	}
	if err != nil || json.Unmarshal(line, &capture) != nil || !capture.Valid() || !snapshotID.MatchString(capture.Snapshot) {
		return Source{}, ErrIncomplete
	}
	dump := launch(exec.CommandContext(ctx, filepath.Join(bin, "pg_dump"), append(connection,
		"--format=custom", "--no-password", "--lock-wait-timeout=5s", "--no-subscriptions", "--no-publications",
		"--no-tablespaces", "--no-security-labels", "--snapshot="+capture.Snapshot)...))
	dump.Stdout = &BoundedWriter{Writer: dst}
	if err := dump.Run(); err != nil {
		return Source{}, err
	}
	return capture.Source, nil
}

// BoundedWriter limits dump data in both the guest and the control plane.
type BoundedWriter struct {
	io.Writer
	Bytes int64
}

func (w *BoundedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > MaxBytes-w.Bytes {
		return 0, ErrTooLarge
	}
	n, err := w.Writer.Write(p)
	w.Bytes += int64(n)
	return n, err
}

// StreamWriter frames dump data. Finish is the only way to send a success
// trailer, so disconnects and failed processes cannot publish partial dumps.
type StreamWriter struct{ io.Writer }

func (s StreamWriter) Write(p []byte) (int, error) {
	var sent int
	for len(p) != 0 {
		block := p[:min(len(p), Chunk)]
		if err := binary.Write(s.Writer, binary.BigEndian, uint32(len(block))); err != nil {
			return sent, err
		}
		n, err := s.Writer.Write(block)
		sent += n
		if err != nil {
			return sent, err
		}
		if n != len(block) {
			return sent, io.ErrShortWrite
		}
		p = p[n:]
	}
	return sent, nil
}

func (s StreamWriter) Finish(source Source) error {
	if !source.Valid() {
		return ErrIncomplete
	}
	if err := binary.Write(s.Writer, binary.BigEndian, uint32(0)); err != nil {
		return err
	}
	return json.NewEncoder(s.Writer).Encode(source)
}

func ReadStream(r io.Reader, dst io.Writer) (Source, error) {
	body := &BoundedWriter{Writer: dst}
	buf := make([]byte, Chunk)
	for {
		var size uint32
		if err := binary.Read(r, binary.BigEndian, &size); err != nil {
			return Source{}, ErrIncomplete
		}
		if size == 0 {
			break
		}
		if size > Chunk {
			return Source{}, ErrIncomplete
		}
		block := buf[:size]
		if _, err := io.ReadFull(r, block); err != nil {
			return Source{}, ErrIncomplete
		}
		if n, err := body.Write(block); err != nil {
			return Source{}, err
		} else if n != len(block) {
			return Source{}, io.ErrShortWrite
		}
	}
	if body.Bytes < 5 {
		return Source{}, ErrIncomplete
	}
	line, err := bufio.NewReader(io.LimitReader(r, Chunk)).ReadBytes('\n')
	if err != nil {
		return Source{}, ErrIncomplete
	}
	return DecodeSource(line)
}
