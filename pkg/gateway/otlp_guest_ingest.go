package gateway

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"

	"github.com/google/uuid"
)

// ErrOTLPExportInvalid reports an export that cannot be decoded. Callers map
// it to a non-retryable outcome.
var ErrOTLPExportInvalid = errors.New("invalid OTLP trace export")

// AddOTLPTraceExport decodes one OTLP/HTTP trace export body received outside
// the public handler (the ADR-958 in-guest bridge) and merges its spans into
// the accumulator for accountID. It applies the public endpoint's codec and
// decoded-size bound; traces already claimed by another account are counted
// as rejected without discarding the rest of the export.
func (s *SpansAccumulator) AddOTLPTraceExport(accountID uuid.UUID, contentType, contentEncoding string, body []byte, maxDecodedBytes int) (accepted, rejected int, err error) {
	mediaType, _, parseErr := mime.ParseMediaType(contentType)
	if parseErr != nil || (mediaType != "application/json" && mediaType != "application/x-protobuf") {
		return 0, 0, fmt.Errorf("%w: unsupported content type", ErrOTLPExportInvalid)
	}
	raw, err := decodeOTLPContentEncoding(contentEncoding, body, maxDecodedBytes)
	if err != nil {
		return 0, 0, err
	}
	request, err := otlpTraceCodec{contentType: mediaType}.decode(raw)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %w", ErrOTLPExportInvalid, err)
	}
	batches, err := extractTraceBatches(request)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %w", ErrOTLPExportInvalid, err)
	}
	for _, batch := range batches {
		if _, addErr := s.Add(batch.traceID, accountID, batch.spans); addErr != nil {
			rejected += len(batch.spans)
			continue
		}
		accepted += len(batch.spans)
	}
	return accepted, rejected, nil
}

func decodeOTLPContentEncoding(contentEncoding string, body []byte, maxDecodedBytes int) ([]byte, error) {
	var reader io.Reader = bytes.NewReader(body)
	switch strings.ToLower(strings.TrimSpace(contentEncoding)) {
	case "", "identity":
	case "gzip":
		decoded, err := gzip.NewReader(reader)
		if err != nil {
			return nil, fmt.Errorf("%w: decode gzip: %w", ErrOTLPExportInvalid, err)
		}
		defer func() { _ = decoded.Close() }()
		reader = decoded
	default:
		return nil, fmt.Errorf("%w: unsupported content encoding", ErrOTLPExportInvalid)
	}
	raw, err := io.ReadAll(io.LimitReader(reader, int64(maxDecodedBytes)+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %w", ErrOTLPExportInvalid, err)
	}
	if len(raw) > maxDecodedBytes {
		return nil, fmt.Errorf("%w: body exceeds ingestion limit", ErrOTLPExportInvalid)
	}
	return raw, nil
}
