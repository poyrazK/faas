package s3gateway

import (
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/prometheus/client_golang/prometheus"
)

func (h *DiskHandler) writeAdmissionProbe(w http.ResponseWriter, r *http.Request, id string) {
	w.Header().Set("Cache-Control", "no-store")
	bucket, key, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	size, err := replicationNumber(r.Header.Get(objectstorage.OwnedNodeAdmissionSizeHeader), 0, api.MaxObjectUploadBytes)
	if err != nil || r.Method != http.MethodHead || key != "" || !objectnode.ValidBucket(bucket) || len(operationQuery(r.URL.Query())) != 0 || r.ContentLength != 0 || r.Header.Get("Range") != "" || r.Header.Get("If-Match") != "" || r.Header.Get("If-None-Match") != "" || r.Header.Get("If-Modified-Since") != "" || r.Header.Get("If-Unmodified-Since") != "" {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	if err = h.config.Store.CanMutate(); err == nil {
		err = h.config.Store.HeadBucket(bucket)
	}
	if err == nil {
		err = h.config.Store.CheckWriteAdmission(r.Header.Get(objectstorage.OwnedNodeAdmissionHeader), size)
	}
	if err != nil {
		if errors.Is(err, objectnode.ErrWritePressure) {
			w.Header().Set(objectstorage.OwnedNodeAdmissionStatusHeader, "blocked")
		}
		h.failure(w, r, id, err)
		return
	}
	w.Header().Set(objectstorage.OwnedNodeAdmissionStatusHeader, "ready")
	w.WriteHeader(http.StatusOK)
}

func registerWriteAdmissionMetrics(h *DiskHandler, registry *prometheus.Registry) {
	for _, kind := range []string{"enabled", "blocked", "active", "accepted_total", "rejected_total", "reserved_records", "reserved_replay_bytes", "reserved_disk_bytes"} {
		value := func() float64 {
			s := h.config.Store.AdmissionStatus()
			switch kind {
			case "enabled":
				if s.Enabled {
					return 1
				}
			case "blocked":
				if s.Blocked {
					return 1
				}
			case "active":
				return float64(s.Active)
			case "accepted_total":
				return float64(s.Accepted)
			case "rejected_total":
				return float64(s.Rejected)
			case "reserved_records":
				return float64(s.ReservedRecords)
			case "reserved_replay_bytes":
				return float64(s.ReservedReplayBytes)
			case "reserved_disk_bytes":
				return float64(s.ReservedDiskBytes)
			}
			return 0
		}
		name, help := "faas_object_node_write_admission_"+kind, "Cached write admission observation: "+kind+"; no object or request labels."
		if strings.HasSuffix(kind, "_total") {
			registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: name, Help: help}, value))
		} else {
			registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: name, Help: help}, value))
		}
	}
	for name, bit := range map[string]uint64{"lag": objectnode.AdmissionLag, "records": objectnode.AdmissionRecords, "journal_bytes": objectnode.AdmissionJournal, "replay_bytes": objectnode.AdmissionReplay, "free_bytes": objectnode.AdmissionFree, "disk_probe": objectnode.AdmissionDiskProbe} {
		registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "faas_object_node_write_admission_pressure_" + name, Help: "Whether this bounded pressure reason is latched."}, func() float64 {
			if h.config.Store.AdmissionStatus().Reasons&bit != 0 {
				return 1
			}
			return 0
		}))
	}
}
