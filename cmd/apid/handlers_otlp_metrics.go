package main

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"time"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/otlpmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

// postOTLPMetrics serves POST /v1/apps/{slug}/otlp/v1/metrics (ADR-745):
// OTLP/HTTP metric exports, in protobuf or JSON, stored as the app's custom
// metrics. Anything outside the supported subset is reported through OTLP
// partial success rather than failing the whole export.
func (s *server) postOTLPMetrics(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.customMetricHistoryEnabled {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "custom_metric_history_unavailable",
			"OTLP metrics unavailable", "custom metric history is not enabled for this deployment"))
		return
	}
	if !acct.Plan.CustomMetricsAllowed() {
		api.WriteProblem(w, api.ErrPlanCustomMetricsNotAllowed(acct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug")) //nolint:contextcheck // loadApp uses r.Context().
	if !ok {
		return
	}
	store, ok := s.store.(state.CustomMetricKindStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("custom metric storage unavailable"))
		return
	}
	req, jsonBody, problem := decodeOTLPMetrics(w, r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	res := otlpmetrics.Translate(req, time.Now().UTC())
	for _, p := range res.Points {
		err := store.PutCustomMetricOfKind(r.Context(), app.ID, p.Name, p.Kind, p.Value, p.ObservedAt, api.MaxCustomMetricsPerApp)
		switch {
		case errors.Is(err, state.ErrCustomMetricLimit):
			res.Rejected++
			if res.Reason == "" {
				res.Reason = fmt.Sprintf("metric %q: app is at its limit of %d custom metrics", p.Name, api.MaxCustomMetricsPerApp)
			}
		case err != nil:
			writeCustomerInternalProblem(w, r, s.log, "store OTLP metrics",
				"Gregale could not store these metrics.", "Retry the export; OTLP exporters retry automatically.", err)
			return
		}
	}
	writeOTLPMetricsResponse(w, res, jsonBody)
}

// decodeOTLPMetrics reads a bounded body as protobuf (the OTLP/HTTP default)
// or JSON, reporting which so the response uses the same encoding.
func decodeOTLPMetrics(w http.ResponseWriter, r *http.Request) (*colmetricspb.ExportMetricsServiceRequest, bool, *api.Problem) {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	jsonBody := mediaType == "application/json"
	if !jsonBody && mediaType != "application/x-protobuf" {
		return nil, false, api.NewProblem(http.StatusUnsupportedMediaType, api.CodeValidation, "Unsupported content type",
			"send OTLP metrics as application/x-protobuf or application/json")
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, api.OTLPMetricsMaxBodyBytes))
	if err != nil {
		return nil, false, api.NewProblem(http.StatusRequestEntityTooLarge, api.CodeValidation, "Export too large",
			fmt.Sprintf("an OTLP metrics export may be at most %d bytes", api.OTLPMetricsMaxBodyBytes))
	}
	req := &colmetricspb.ExportMetricsServiceRequest{}
	if jsonBody {
		err = protojson.Unmarshal(body, req)
	} else {
		err = proto.Unmarshal(body, req)
	}
	if err != nil {
		return nil, false, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid OTLP metrics export",
			"the body is not a valid ExportMetricsServiceRequest")
	}
	return req, jsonBody, nil
}

func writeOTLPMetricsResponse(w http.ResponseWriter, res otlpmetrics.Result, jsonBody bool) {
	resp := &colmetricspb.ExportMetricsServiceResponse{}
	if res.Rejected > 0 {
		resp.PartialSuccess = &colmetricspb.ExportMetricsPartialSuccess{RejectedDataPoints: res.Rejected, ErrorMessage: res.Reason}
	}
	var body []byte
	var err error
	if jsonBody {
		w.Header().Set("Content-Type", "application/json")
		body, err = protojson.Marshal(resp)
	} else {
		w.Header().Set("Content-Type", "application/x-protobuf")
		body, err = proto.Marshal(resp)
	}
	if err != nil {
		http.Error(w, "encode response", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
