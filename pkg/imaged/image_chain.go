package imaged

// adr: 430

import (
	"context"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/imagechain"
)

func closeContainerLayers(readers []io.ReadCloser) {
	for _, reader := range readers {
		_ = reader.Close()
	}
}

func wrapContainerLayerReaders(ctx context.Context, prepared preparedContainerWorkload, readers []io.ReadCloser, start int) ([]io.ReadCloser, error) {
	// Historical producers lacking retained bytes cannot obtain validated
	// lineage by constructing wrappers or inventing consumption counters.
	if prepared.Evidence == nil {
		return readers, nil
	}
	image, err := imagechain.Validate(prepared.Evidence, prepared.SourceDigest, prepared.Digest)
	if err != nil {
		return nil, err
	}
	if start < 0 || start > len(image.Layers) || len(readers) != len(image.Layers)-start {
		return nil, fmt.Errorf("imaged: consumed layer count does not match retained image")
	}
	out := make([]io.ReadCloser, len(readers))
	for i, reader := range readers {
		out[i], err = imagechain.NewLayerStream(ctx, reader, image.Layers[start+i], image.DiffIDs[start+i], start+i)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func consumedContainerLayers(prepared preparedContainerWorkload, readers []io.Reader, start int) (preparedContainerWorkload, error) {
	if prepared.Evidence == nil {
		return prepared, nil
	}
	image, err := imagechain.Validate(prepared.Evidence, prepared.SourceDigest, prepared.Digest)
	if err != nil {
		return prepared, err
	}
	layers := make([]imagechain.LayerConsumption, len(readers))
	for i, reader := range readers {
		stream, ok := reader.(interface {
			Consumption() (imagechain.LayerConsumption, error)
		})
		if !ok {
			return prepared, fmt.Errorf("imaged: verified layer consumer unavailable")
		}
		layers[i], err = stream.Consumption()
		if err != nil {
			return prepared, fmt.Errorf("imaged: incomplete layer %d: %w", i+start, err)
		}
	}
	if err := imagechain.ValidateConsumption(image, start, layers); err != nil {
		return prepared, err
	}
	prepared.LayerStart, prepared.Layers = start, layers
	return prepared, nil
}
