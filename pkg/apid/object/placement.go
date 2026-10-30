package object

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// reservePlacedBucket reserves b on one of its region's pool backends
// (ADR-20261029174627). The store picks the backend under its region lock,
// so concurrent creations never pass a backend's ceiling.
func (s *Service) reservePlacedBucket(ctx context.Context, st state.ObjectBucketStore, b state.ObjectBucket) (state.ObjectBucket, error) {
	placer, ok := st.(state.ObjectBucketPlacementStore)
	if !ok {
		return state.ObjectBucket{}, objectstorage.ErrUnavailable
	}
	backends, err := s.Host.ObjectStorage().Placements(b.Region)
	if err != nil {
		return state.ObjectBucket{}, err
	}
	reserved, _, err := placer.ReserveObjectBucketPlaced(ctx, b, s.Host.ObjectStorage().MaxBucketsPerApp, placementCandidates(backends))
	if errors.Is(err, state.ErrObjectStorageRegionFull) {
		s.Host.Logger().Warn("object storage region has no backend with room for a new bucket", "region", b.Region)
	}
	return reserved, err
}

func placementCandidates(backends []objectstorage.Backend) []state.ObjectBucketPlacement {
	out := make([]state.ObjectBucketPlacement, len(backends))
	for i, b := range backends {
		out[i] = state.ObjectBucketPlacement{BackendID: b.ID, BackendFingerprint: b.Fingerprint, MaxBuckets: b.MaxBuckets}
	}
	return out
}

// placementMetrics show how full each region's placement pool is
// (ADR-20261029174627).
type placementMetrics struct {
	buckets *prometheus.GaugeVec
	limits  *prometheus.GaugeVec
	full    *prometheus.GaugeVec
}

func newPlacementMetrics() placementMetrics {
	return placementMetrics{
		buckets: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "faas_object_storage_backend_buckets", Help: "Live buckets on each configured object storage backend."}, []string{"region", "backend"}),
		limits:  prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "faas_object_storage_backend_bucket_limit", Help: "max_buckets of each backend that receives new buckets, when it has one."}, []string{"region", "backend"}),
		full:    prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "faas_object_storage_region_full", Help: "1 while no backend in the region's pool has room for a new bucket, so bucket creation is refused."}, []string{"region"}),
	}
}

func (m placementMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{m.buckets, m.limits, m.full}
}

// observePlacement refreshes the placement gauges from the bucket catalog.
func (s *Service) observePlacement(parent context.Context, m placementMetrics) error {
	registry := s.Host.ObjectStorage()
	placer, ok := s.Host.Store().(state.ObjectBucketPlacementStore)
	if registry == nil || !ok {
		return nil
	}
	backends := registry.Backends()
	ids := make([]string, len(backends))
	for i, b := range backends {
		ids[i] = b.ID
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	counts, err := placer.ObjectBucketPlacementCounts(ctx, "", ids)
	if err != nil {
		return err
	}
	for _, b := range backends {
		m.buckets.WithLabelValues(b.Region, b.ID).Set(float64(counts[b.ID].Buckets))
	}
	for _, region := range registry.Regions() {
		placements, err := registry.Placements(region)
		if err != nil {
			return err
		}
		for _, b := range placements {
			if b.MaxBuckets > 0 {
				m.limits.WithLabelValues(region, b.ID).Set(float64(b.MaxBuckets))
			}
		}
		full := 0.0
		if _, room := state.ChooseObjectBucketPlacement(placementCandidates(placements), counts); !room {
			full = 1
		}
		m.full.WithLabelValues(region).Set(full)
	}
	return nil
}
