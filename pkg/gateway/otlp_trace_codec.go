package gateway

import (
	"compress/gzip"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type otlpTraceCodec struct {
	contentType string
	legacyJSON  bool
}

func traceCodec(r *http.Request) (otlpTraceCodec, error) {
	codec := otlpTraceCodec{contentType: "application/json"}
	if r.Header.Get("Content-Type") == "" {
		// Historical Gregale clients sent ordinary protobuf JSON without a
		// media type. Keep that input profile separate from standard OTLP JSON.
		codec.legacyJSON = true
		return codec, nil
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return codec, fmt.Errorf("invalid Content-Type")
	}
	if mediaType != "application/json" && mediaType != "application/x-protobuf" {
		return codec, fmt.Errorf("expected application/json or application/x-protobuf")
	}
	codec.contentType = mediaType
	return codec, nil
}

func (c otlpTraceCodec) decode(raw []byte) (*collectortracepb.ExportTraceServiceRequest, error) {
	var req collectortracepb.ExportTraceServiceRequest
	if c.legacyJSON {
		err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &req)
		return &req, err
	}
	if c.contentType == "application/x-protobuf" {
		return &req, proto.Unmarshal(raw, &req)
	}
	// The Collector codec implements OTLP's deviations from protobuf JSON:
	// hexadecimal IDs, numeric enums, and ignored unknown fields.
	export := ptraceotlp.NewExportRequest()
	if err := export.UnmarshalJSON(raw); err != nil {
		return nil, fmt.Errorf("decode OTLP JSON: %w", err)
	}
	wire, err := export.MarshalProto()
	if err != nil {
		return nil, fmt.Errorf("encode decoded OTLP: %w", err)
	}
	return &req, proto.Unmarshal(wire, &req)
}

func (c otlpTraceCodec) write(w http.ResponseWriter, code int, message proto.Message) {
	var body []byte
	var err error
	if c.contentType == "application/x-protobuf" {
		body, err = proto.Marshal(message)
	} else {
		body, err = protojson.Marshal(message)
	}
	if err != nil {
		http.Error(w, "encode OTLP response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", c.contentType)
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

func (c otlpTraceCodec) problem(w http.ResponseWriter, code int, message string) {
	c.write(w, code, &statuspb.Status{Message: message})
}

func readOTLPTraceBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	// Preserve the existing 4 MiB ingestion bound for both the compressed
	// body and its decoded representation.
	const bodyCap = 4 << 20
	r.Body = http.MaxBytesReader(w, r.Body, bodyCap)
	var reader io.Reader = r.Body
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Content-Encoding")), "gzip") {
		decoded, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, fmt.Errorf("decode gzip: %w", err)
		}
		defer func() { _ = decoded.Close() }()
		reader = decoded
	}
	body, err := io.ReadAll(io.LimitReader(reader, bodyCap+1))
	if err != nil {
		return nil, fmt.Errorf("read OTLP body: %w", err)
	}
	if len(body) > bodyCap {
		return nil, fmt.Errorf("OTLP body exceeds ingestion limit")
	}
	return body, nil
}
