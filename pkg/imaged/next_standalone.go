package imaged

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/sharedruntime"
)

// nextStandaloneEntry reports whether a filtered /app entry belongs in a
// Next.js standalone app layer (ADR-20261124160000): the standalone tree and
// the directories above it.
func nextStandaloneEntry(name string) bool {
	prefix := sharedruntime.NextStandalonePrefix
	return name == "app" || name == "app/.next" || name == prefix || strings.HasPrefix(name, prefix+"/")
}

// selectNextStandalone re-reads layers that filterAppLayers wrote and keeps
// only the standalone tree, which must hold the server. On an error the
// input layers are left where they were read to and the caller rewinds
// them.
func selectNextStandalone(layers []io.Reader) ([]io.Reader, func(), error) {
	keep := nextStandaloneEntry
	tmpDir, err := os.MkdirTemp("", "faas-next-standalone-")
	if err != nil {
		return nil, func() {}, fmt.Errorf("create standalone layer tempdir: %w", err)
	}
	var files []*os.File
	cleanup := func() {
		for _, file := range files {
			_ = file.Close()
		}
		_ = os.RemoveAll(tmpDir)
	}
	fail := func(err error) ([]io.Reader, func(), error) {
		cleanup()
		return nil, func() {}, err
	}
	sawServer := false
	for i, layer := range layers {
		out, err := os.Create(filepath.Join(tmpDir, fmt.Sprintf("layer-%03d.tar.zst", i)))
		if err != nil {
			return fail(fmt.Errorf("create standalone layer %d: %w", i, err))
		}
		files = append(files, out)
		entries, server, err := copyNextStandaloneLayer(layer, out, keep)
		if err != nil {
			return fail(fmt.Errorf("standalone layer %d: %w", i, err))
		}
		sawServer = sawServer || server
		if entries == 0 {
			files = files[:len(files)-1]
			_ = out.Close()
			_ = os.Remove(out.Name())
			continue
		}
		if _, err := out.Seek(0, io.SeekStart); err != nil {
			return fail(fmt.Errorf("rewind standalone layer %d: %w", i, err))
		}
	}
	if !sawServer {
		return fail(errors.New("no standalone server in the app layer"))
	}
	readers := make([]io.Reader, len(files))
	for i, file := range files {
		readers[i] = file
	}
	return readers, cleanup, nil
}

// copyNextStandaloneLayer copies one layer's entries that keep accepts to out.
func copyNextStandaloneLayer(layer io.Reader, out io.Writer, keep func(string) bool) (int, bool, error) {
	zr, err := rootfs.OpenCompressedLayer(layer)
	if err != nil {
		return 0, false, fmt.Errorf("open layer: %w", err)
	}
	defer func() { _ = zr.Close() }()
	zw, err := rootfs.NewLayerWriter(out)
	if err != nil {
		return 0, false, fmt.Errorf("compress layer: %w", err)
	}
	tw := tar.NewWriter(zw)
	tr := tar.NewReader(zr)
	entries, server := 0, false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = tw.Close()
			_ = zw.Close()
			return 0, false, fmt.Errorf("read layer: %w", err)
		}
		name := pathpkg.Clean(hdr.Name)
		if !keep(name) {
			continue
		}
		if hdr.Typeflag == tar.TypeLink && !keep(pathpkg.Clean(hdr.Linkname)) {
			_ = tw.Close()
			_ = zw.Close()
			return 0, false, fmt.Errorf("hardlink %q points at a dropped entry", hdr.Linkname)
		}
		server = server || (name == sharedruntime.NextStandaloneServer && hdr.Typeflag == tar.TypeReg)
		if err := tw.WriteHeader(hdr); err != nil {
			_ = tw.Close()
			_ = zw.Close()
			return 0, false, fmt.Errorf("write %s header: %w", name, err)
		}
		if _, err := io.CopyN(tw, tr, hdr.Size); err != nil {
			_ = tw.Close()
			_ = zw.Close()
			return 0, false, fmt.Errorf("write %s: %w", name, err)
		}
		entries++
	}
	if err := errors.Join(tw.Close(), zw.Close()); err != nil {
		return 0, false, fmt.Errorf("close layer: %w", err)
	}
	return entries, server, nil
}

// rewindLayers seeks filtered layer files back to their start.
func rewindLayers(layers []io.Reader) error {
	for i, layer := range layers {
		seeker, ok := layer.(io.Seeker)
		if !ok {
			return fmt.Errorf("layer %d cannot be re-read", i)
		}
		if _, err := seeker.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("rewind layer %d: %w", i, err)
		}
	}
	return nil
}
