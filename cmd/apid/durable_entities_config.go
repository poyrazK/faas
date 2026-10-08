package main

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

func (s *server) configureDurableEntities(ctx context.Context, getenv func(string) string) error {
	if getenv("FAAS_DURABLE_ENTITIES_ENABLED") != "1" {
		if getenv("FAAS_DURABLE_ENTITY_ALARMS_ENABLED") == "1" {
			return errors.New("durable entity alarms require the invocation preview")
		}
		if getenv("FAAS_DURABLE_ENTITY_MAINTENANCE_ENABLED") == "1" {
			return errors.New("durable entity maintenance requires the invocation preview")
		}
		return nil
	}
	backend, err := s.objectStorage.Resolve(getenv("FAAS_DURABLE_ENTITY_BACKEND"), getenv("FAAS_DURABLE_ENTITY_BACKEND_FINGERPRINT"))
	if err != nil {
		return errors.New("select a configured backend and its placement fingerprint")
	}
	provider, ok := backend.Provider.(objectstorage.ConditionalStateProvider)
	if !ok {
		return durableentity.ErrUnsupported
	}
	store, err := durableentity.NewProviderStore(provider, getenv("FAAS_DURABLE_ENTITY_BUCKET"))
	if err != nil {
		return err
	}
	apps, err := durableEntityAppAllowlist(getenv("FAAS_DURABLE_ENTITY_APPS"))
	if err != nil {
		return err
	}
	limit, err := durableEntityStorageLimit(getenv("FAAS_DURABLE_ENTITY_MAX_RETAINED_BYTES"))
	if err != nil {
		return err
	}
	// The request budget is shorter than this lease. We deliberately avoid a
	// renewal writer racing every transition in the first synchronous slice.
	probeCtx, cancel := context.WithTimeout(ctx, api.DurableEntityInvokeTimeout)
	defer cancel()
	observed := durableEntityObservedStore{ObjectStore: store, metrics: func() *durableEntityMetrics { return s.durableEntityMetrics }}
	engine, err := durableentity.Open(probeCtx, observed, durableentity.Options{LeaseDuration: api.MaxDurableEntityLease, RetainedBytesLimit: limit})
	if err != nil {
		return errors.New("durable entities require private conditional writes and strongly consistent reads")
	}
	alarms := getenv("FAAS_DURABLE_ENTITY_ALARMS_ENABLED") == "1"
	if alarms {
		if err := engine.CheckAlarmDiscovery(probeCtx); err != nil {
			return errors.New("durable entity alarms require private delimiter listing")
		}
	}
	maintenance := getenv("FAAS_DURABLE_ENTITY_MAINTENANCE_ENABLED") == "1"
	if maintenance {
		if err := engine.CheckMaintenance(probeCtx); err != nil {
			return errors.New("durable entity maintenance requires private delimiter/flat listing and deletion")
		}
	}
	s.durableEntities, s.durableEntityApps, s.durableEntityOwner = engine, apps, uuid.NewString()
	s.durableEntityAlarmsEnabled = alarms
	s.durableEntityMaintenanceEnabled = maintenance
	return nil
}

func durableEntityStorageLimit(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	limit, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || limit <= 0 || strconv.FormatInt(limit, 10) != raw {
		return 0, errors.New("FAAS_DURABLE_ENTITY_MAX_RETAINED_BYTES must be an explicit positive byte count")
	}
	return limit, nil
}

func durableEntityAppAllowlist(raw string) (map[string]bool, error) {
	apps := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		id, err := uuid.Parse(strings.TrimSpace(part))
		if err != nil || id == uuid.Nil {
			return nil, errors.New("FAAS_DURABLE_ENTITY_APPS must contain explicit app UUIDs")
		}
		apps[id.String()] = true
	}
	return apps, nil
}
