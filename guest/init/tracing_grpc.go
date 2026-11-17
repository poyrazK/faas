package main

// Guest-local OTLP/gRPC receiver (ADR-20261108100000). SDKs and collectors
// that export over gRPC (the default for .NET, many Collector setups and
// some Java and Rust configurations) reach 127.0.0.1:4317 the same way the
// OTLP/HTTP ones reach :4318. OTLP's Export calls are unary, so this serves
// them with net/http's cleartext HTTP/2 rather than a gRPC server: it takes
// the one length-prefixed message off the request and hands the protobuf
// bytes, unparsed, to the same export path as OTLP/HTTP protobuf.

import (
	"encoding/binary"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	otlpGRPCTracesMethod = "/opentelemetry.proto.collector.trace.v1.TraceService/Export"
	otlpGRPCLogsMethod   = "/opentelemetry.proto.collector.logs.v1.LogsService/Export"
	// ADR-20261108160000 slice B.
	otlpGRPCMetricsMethod = "/opentelemetry.proto.collector.metrics.v1.MetricsService/Export"
)

// gRPC status codes the receiver answers with.
const (
	grpcOK                = 0
	grpcInvalidArgument   = 3
	grpcResourceExhausted = 8
	grpcUnimplemented     = 12
	grpcUnavailable       = 14
)

// grpcEmptyExportResponse is an empty Export*ServiceResponse as one
// uncompressed gRPC message: flag 0, length 0.
var grpcEmptyExportResponse = []byte{0, 0, 0, 0, 0}

// newTraceGRPCServer serves handler over cleartext HTTP/2 only, with prior
// knowledge, as gRPC clients connect.
func newTraceGRPCServer(handler http.Handler) *http.Server {
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	return &http.Server{
		Handler:           handler,
		Protocols:         protocols,
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       api.TraceTransportTimeout,
		WriteTimeout:      2 * api.TraceTransportTimeout,
	}
}

func (b *traceBridge) grpcHandler() http.Handler {
	return http.HandlerFunc(b.grpcExport)
}

func (b *traceBridge) grpcExport(w http.ResponseWriter, r *http.Request) {
	if r.ProtoMajor != 2 {
		http.Error(w, "OTLP/gRPC needs HTTP/2", http.StatusHTTPVersionNotSupported)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !isGRPCContentType(r.Header.Get("Content-Type")) {
		http.Error(w, "expected application/grpc", http.StatusUnsupportedMediaType)
		return
	}
	var signal byte
	switch r.URL.Path {
	case otlpGRPCTracesMethod:
	case otlpGRPCLogsMethod:
		signal = api.TraceCodecLogsSignal
	case otlpGRPCMetricsMethod:
		signal = api.TraceCodecMetricsSignal
	default:
		writeGRPCStatus(w, grpcUnimplemented, "only OTLP trace, log and metric Export are served here", false)
		return
	}
	if !b.tryAcquire() {
		writeGRPCStatus(w, grpcUnavailable, "trace bridge busy", false)
		return
	}
	defer b.release()
	r.Body = http.MaxBytesReader(w, r.Body, api.TraceMaxFrameBytes+5)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeGRPCStatus(w, grpcResourceExhausted, "trace export exceeds bounds", false)
			return
		}
		writeGRPCStatus(w, grpcInvalidArgument, "read trace export", false)
		return
	}
	message, compressed, ok := grpcUnaryMessage(body)
	if !ok {
		writeGRPCStatus(w, grpcInvalidArgument, "expected one length-prefixed gRPC message", false)
		return
	}
	codec, encoding := api.TraceCodecProtobuf, ""
	if compressed {
		if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Grpc-Encoding")), "gzip") {
			writeGRPCStatus(w, grpcUnimplemented, "only gzip message compression is supported", false)
			return
		}
		codec, encoding = api.TraceCodecGzipProtobuf, "gzip"
	}
	if len(message) == 0 {
		writeGRPCStatus(w, grpcOK, "", true)
		return
	}
	ack, err := b.export(r.Context(), codec, signal, "application/x-protobuf", encoding, message)
	switch {
	case err != nil:
		writeGRPCStatus(w, grpcUnavailable, "trace host unavailable", false)
	case ack == api.TraceAckAccepted:
		writeGRPCStatus(w, grpcOK, "", true)
	case ack == api.TraceAckLimited:
		// Retryable, as the OTLP/HTTP receiver's 429 is.
		writeGRPCStatus(w, grpcUnavailable, "trace export rate limited", false)
	case ack == api.TraceAckUnavailable:
		writeGRPCStatus(w, grpcUnavailable, "trace host unavailable", false)
	default:
		// Permanent refusals must not be retried; INVALID_ARGUMENT is not
		// retryable in OTLP/gRPC.
		writeGRPCStatus(w, grpcInvalidArgument, "trace export rejected", false)
	}
}

func isGRPCContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && (mediaType == "application/grpc" || mediaType == "application/grpc+proto")
}

// grpcUnaryMessage returns the single message of a unary request body:
// [1B compressed flag][4B BE length][message].
func grpcUnaryMessage(body []byte) (message []byte, compressed, ok bool) {
	if len(body) < 5 || body[0] > 1 {
		return nil, false, false
	}
	n := binary.BigEndian.Uint32(body[1:5])
	if uint64(n) != uint64(len(body)-5) {
		return nil, false, false
	}
	return body[5:], body[0] == 1, true
}

// writeGRPCStatus answers with the status in trailers, after the empty
// export response when withResponse is set.
func writeGRPCStatus(w http.ResponseWriter, code int, message string, withResponse bool) {
	h := w.Header()
	h.Set("Content-Type", "application/grpc")
	w.WriteHeader(http.StatusOK)
	if withResponse {
		_, _ = w.Write(grpcEmptyExportResponse)
	}
	// Set after the header is written, so net/http sends them as trailers.
	h.Set(http.TrailerPrefix+"Grpc-Status", strconv.Itoa(code))
	if message != "" {
		h.Set(http.TrailerPrefix+"Grpc-Message", message)
	}
}
