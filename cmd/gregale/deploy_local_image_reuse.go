package main

// A local-image redeploy leaves out the layers the app's previous uploaded
// image already has (ADR-20261127120000 amendment 1): the CLI asks for the
// app's image base, uploads a thin archive, and apid rebuilds the full one.
// Any problem along the way falls back to the full upload.

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ociarchive"
)

// thinArchive writes the converted image without the layers the app's image
// base holds. It returns the thin archive's path, or "" when nothing can be
// reused: no base, an API without the endpoint, or no shared layer.
func (img *localImageDeploy) thinArchive(ctx context.Context, client *Client, slug string) string {
	base, err := client.GetAppImageBase(ctx, slug)
	if err != nil || len(base.Layers) == 0 {
		return ""
	}
	reuse := make(map[string]int64, len(base.Layers))
	for _, l := range base.Layers {
		reuse[l.Digest] = l.Size
	}
	full, err := os.Open(img.archivePath) //nolint:forbidigo // CLI-owned temporary file.
	if err != nil {
		return ""
	}
	defer func() { _ = full.Close() }()
	path := filepath.Join(filepath.Dir(img.archivePath), "image.thin.tar")
	out, err := os.Create(path) //nolint:forbidigo // CLI-owned temporary file.
	if err != nil {
		return ""
	}
	omitted, omittedBytes, err := ociarchive.Thin(full, out, reuse)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil || omitted == 0 {
		_ = os.Remove(path)
		return ""
	}
	if !jsonOutput {
		layers := "layers"
		if omitted == 1 {
			layers = "layer"
		}
		PrintProgress(osStderr, "reusing %d %s from the app's previous image (%d MiB not uploaded)", omitted, layers, omittedBytes/(1024*1024))
	}
	return path
}

// isImageBaseChanged reports a thin upload apid could not fill from the
// app's current image base.
func isImageBaseChanged(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.Problem.Code == api.CodeImageBaseChanged
}
