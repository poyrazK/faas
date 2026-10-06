package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

type recoveryTestProvider struct {
	*fakeProvider
	read  func(RestoreSourceDefinition) (RestoreSourceObservation, error)
	reads int
}

func (p *recoveryTestProvider) ObserveRestoreSource(_ context.Context, d RestoreSourceDefinition) (RestoreSourceObservation, error) {
	p.reads++
	return p.read(d)
}

func recoveryFixture(t *testing.T) (*Service, *MemoryStore, *recoveryTestProvider, Database, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	p := &recoveryTestProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}}
	p.read = func(d RestoreSourceDefinition) (RestoreSourceObservation, error) {
		return RestoreSourceObservation{ProviderResourceID: d.ProviderResourceID, DataResourceID: d.DataResourceID,
			Status: ProviderStatusReady, RetentionSeconds: 3600, HistoryNotBefore: now.Add(-24 * time.Hour)}, nil
	}
	store := NewMemoryStore()
	s := testService(t, testRegistry(t, p, nil), store)
	s.now = func() time.Time { return now }
	d, err := s.Create(t.Context(), CreateRequest{AccountID: "account-a", Name: "source", Spec: testSpec()})
	if err != nil {
		t.Fatal(err)
	}
	return s, store, p, d, &now
}

func TestRestorePreflightRejectsBeforeReservation(t *testing.T) {
	for _, fault := range []string{"shortened retention", "pre-history", "disabled", "missing history", "wrong dataset", "wrong owner", "future metadata", "overflow", "missing resource", "provider outage", "reported gap", "history expired", "bad bounds"} {
		t.Run(fault, func(t *testing.T) {
			s, store, p, source, now := recoveryFixture(t)
			point := now.Add(-30 * time.Minute)
			base := p.read
			want := ErrUnavailable
			p.read = func(d RestoreSourceDefinition) (RestoreSourceObservation, error) {
				o, _ := base(d)
				switch fault {
				case "shortened retention":
					want = ErrInvalid
				case "pre-history":
					o.HistoryNotBefore = now.Add(-time.Minute)
					want = ErrInvalid
				case "disabled":
					o.RetentionSeconds = 0
					want = ErrInvalid
				case "missing history":
					o.HistoryNotBefore = time.Time{}
				case "wrong dataset":
					o.DataResourceID = "replacement"
				case "wrong owner":
					o.ProviderResourceID = "replacement"
				case "future metadata":
					o.HistoryNotBefore = now.Add(time.Hour)
				case "overflow":
					o.RetentionSeconds = 1<<63 - 1
				case "missing resource":
					return o, ErrNotFound
				case "provider outage":
					return o, errors.New("private-provider-password")
				case "reported gap":
					o.HistoryBounds = &RestoreHistoryBounds{From: now.Add(-time.Hour), Through: now.Add(-45 * time.Minute)}
					want = ErrInvalid
				case "history expired":
					o.HistoryBounds = &RestoreHistoryBounds{From: now.Add(-2 * time.Hour), Through: now.Add(-90 * time.Minute)}
					want = ErrInvalid
				case "bad bounds":
					o.HistoryBounds = &RestoreHistoryBounds{From: now.Add(-time.Minute), Through: now.Add(-time.Hour)}
				}
				return o, nil
			}
			// Make the point selection before invoking the service, including the
			// shortened-retention case which still lies inside the catalog limit.
			if fault == "shortened retention" {
				point = now.Add(-2 * time.Hour)
			}
			_, created, err := s.RestoreWithResult(t.Context(), RestoreDatabaseRequest{AccountID: source.AccountID, SourceDatabaseID: source.ID, Name: "target", PointInTime: point})
			if !errors.Is(err, want) || created || p.restoreCalls != 0 {
				t.Fatal("invalid intent reached provider", created, err, p.restoreCalls)
			}
			if _, err := store.FindByName(t.Context(), source.AccountID, "target"); !errors.Is(err, ErrNotFound) {
				t.Fatal("invalid intent left reservation", err)
			}
		})
	}
}

func TestRecoveryStatusKeepsLimitsSeparateFromAvailability(t *testing.T) {
	s, _, p, source, now := recoveryFixture(t)
	r, err := s.GetRecoveryStatus(t.Context(), source.AccountID, source.ID)
	if err != nil || r.Status != "limits_known" || !r.Fresh || r.HistoryBoundsKnown || r.RetentionSeconds != 3600 || !r.EarliestPossibleTime.Equal(now.Add(-time.Hour)) {
		t.Fatal(r, err)
	}
	if r.admits(r.EarliestPossibleTime) != nil || r.admits(r.EarliestPossibleTime.Add(-time.Nanosecond)) == nil || r.admits(*now) == nil {
		t.Fatal("boundary validation")
	}
	base := p.read
	p.read = func(d RestoreSourceDefinition) (RestoreSourceObservation, error) {
		o, _ := base(d)
		o.HistoryBounds = &RestoreHistoryBounds{From: now.Add(-40 * time.Minute), Through: now.Add(-time.Minute)}
		return o, nil
	}
	r, err = s.GetRecoveryStatus(t.Context(), source.AccountID, source.ID)
	if err != nil || r.Status != "available" || !r.HistoryBoundsKnown || !r.EarliestPossibleTime.Equal(now.Add(-40*time.Minute)) || r.admits(r.LatestPossibleTime) != nil {
		t.Fatal(r, err)
	}
	p.read = func(RestoreSourceDefinition) (RestoreSourceObservation, error) {
		return RestoreSourceObservation{}, errors.New("private-secret")
	}
	r, err = s.GetRecoveryStatus(t.Context(), source.AccountID, source.ID)
	if err != nil || r.Status != "unknown" || r.Fresh || r.LastErrorCode != "provider_unavailable" || !r.EarliestPossibleTime.IsZero() || r.RetentionSeconds != 0 {
		t.Fatal(r, err)
	}
}

func TestRestoreReplayBypassesExpiredAndUnavailablePreflight(t *testing.T) {
	s, _, p, source, now := recoveryFixture(t)
	request := RestoreDatabaseRequest{AccountID: source.AccountID, SourceDatabaseID: source.ID, Name: "target", PointInTime: now.Add(-time.Minute)}
	target, created, err := s.RestoreWithResult(t.Context(), request)
	if err != nil || !created || p.reads != 1 || p.restoreCalls != 1 {
		t.Fatal(target, created, err)
	}
	*now = now.Add(48 * time.Hour)
	p.read = func(RestoreSourceDefinition) (RestoreSourceObservation, error) {
		t.Fatal("historical replay contacted provider metadata")
		return RestoreSourceObservation{}, ErrUnavailable
	}
	replay, created, err := s.RestoreWithResult(t.Context(), request)
	if err != nil || created || replay.ID != target.ID || p.reads != 1 || p.restoreCalls != 1 {
		t.Fatal(replay, created, err)
	}
}

func TestRecoveryStatusScopesAndPinsBeforeProviderReads(t *testing.T) {
	s, store, p, source, _ := recoveryFixture(t)
	if _, err := s.GetRecoveryStatus(t.Context(), "other-account", source.ID); !errors.Is(err, ErrNotFound) || p.reads != 0 {
		t.Fatal(err, p.reads)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.GetRecoveryStatus(ctx, source.AccountID, source.ID); !errors.Is(err, context.Canceled) || p.reads != 0 {
		t.Fatal(err, p.reads)
	}
	store.mu.Lock()
	legacy := store.databases[source.ID]
	legacy.DataResourceID = ""
	store.databases[source.ID] = legacy
	store.mu.Unlock()
	r, err := s.GetRecoveryStatus(t.Context(), source.AccountID, source.ID)
	if err != nil || r.Status != "unsupported" || p.reads != 0 {
		t.Fatal(r, err, p.reads)
	}
}

func TestRecoveryStatusUsesPinnedBackendAndRejectsRestoredSourceDrift(t *testing.T) {
	s, store, p, source, now := recoveryFixture(t)
	s.registry.defaults[source.Spec.Region] = "missing-replacement-default"
	s.provisioningEnabled = func() bool { return false }
	r, err := s.GetRecoveryStatus(t.Context(), source.AccountID, source.ID)
	if err != nil || r.Status != "limits_known" || p.reads != 1 {
		t.Fatal(r, err, p.reads)
	}
	store.mu.Lock()
	d := store.databases[source.ID]
	d.RestoreSourceDatabaseID, d.RestoreSourceResourceID, d.RestorePointInTime = "ancestor", "ancestor-data", now.Add(-time.Hour)
	store.databases[source.ID] = d
	store.mu.Unlock()
	r, err = s.GetRecoveryStatus(t.Context(), source.AccountID, source.ID)
	if err != nil || r.Status != "unknown" || r.Fresh || r.LastErrorCode != "observation_invalid" {
		t.Fatal(r, err)
	}
	base := p.read
	p.read = func(d RestoreSourceDefinition) (RestoreSourceObservation, error) {
		o, _ := base(d)
		o.Lineage = &RestoreLineage{SourceResourceID: "ancestor-data", PointInTime: now.Add(-time.Hour)}
		return o, nil
	}
	r, err = s.GetRecoveryStatus(t.Context(), source.AccountID, source.ID)
	if err != nil || r.Status != "limits_known" {
		t.Fatal(r, err)
	}
}

func TestRecoveryStatusHonorsTimeoutEvenWhenObserverIgnoresIt(t *testing.T) {
	s, _, _, source, _ := recoveryFixture(t)
	s.providerTimeout = time.Nanosecond
	r, err := s.GetRecoveryStatus(t.Context(), source.AccountID, source.ID)
	if err != nil || r.Status != "unknown" || r.Fresh || r.LastErrorCode != "provider_unavailable" {
		t.Fatal(r, err)
	}
}

func TestCapturedRestorePreflightAuthenticatesPinsAndCurrentRetention(t *testing.T) {
	s, store, p, source, now := recoveryFixture(t)
	d := RestoreSourceDefinition{Spec: source.Spec, BackendID: source.BackendID, BackendFingerprint: source.BackendFingerprint, ProviderResourceID: source.ProviderResourceID, DataResourceID: source.DataResourceID}
	if err := s.PreflightRestore(t.Context(), source.AccountID, source.ID, d, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	reads := p.reads
	d.DataResourceID = "replacement"
	if err := s.PreflightRestore(t.Context(), source.AccountID, source.ID, d, now.Add(-time.Minute)); !errors.Is(err, ErrConflict) || p.reads != reads {
		t.Fatal(err, p.reads)
	}
	d.DataResourceID = source.DataResourceID
	store.mu.Lock()
	live := store.databases[source.ID]
	live.Spec.RestoreWindowSeconds = 10
	store.databases[source.ID] = live
	store.mu.Unlock()
	if err := s.PreflightRestore(t.Context(), source.AccountID, source.ID, d, now.Add(-time.Minute)); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

type providerWithoutRecoveryObserver struct{ Provider }

func TestRecoveryStatusRejectsUnqualifiedCapabilitiesAndNotReadySources(t *testing.T) {
	for _, fault := range []string{"missing observer", "missing capability", "catalog updating", "provider pending"} {
		t.Run(fault, func(t *testing.T) {
			s, store, p, source, _ := recoveryFixture(t)
			want := "unsupported"
			reads := 0
			backend := s.registry.backends[source.BackendID]
			switch fault {
			case "missing observer":
				backend.Provider = providerWithoutRecoveryObserver{Provider: p}
			case "missing capability":
				backend.Capabilities.RestorePreflight = false
			case "catalog updating":
				store.mu.Lock()
				d := store.databases[source.ID]
				d.State = StateUpdating
				store.databases[source.ID] = d
				store.mu.Unlock()
				want = "unavailable"
			case "provider pending":
				base := p.read
				p.read = func(d RestoreSourceDefinition) (RestoreSourceObservation, error) {
					o, _ := base(d)
					o.Status = ProviderStatusPending
					return o, nil
				}
				want = "unavailable"
				reads = 1
			}
			s.registry.backends[source.BackendID] = backend
			r, err := s.GetRecoveryStatus(t.Context(), source.AccountID, source.ID)
			if err != nil || r.Status != want || p.reads != reads {
				t.Fatal(r, err, p.reads)
			}
		})
	}
	c := testCapabilities()
	c.PointInTimeRestore = false
	c.RestorePreflight = true
	c.MaxRestoreWindowSeconds = 0
	if !errors.Is(c.Validate(), ErrInvalid) {
		t.Fatal("preflight without PITR capability accepted")
	}
}

func TestCapturedRestorePreflightRechecksCurrentRetentionAfterMetadataRead(t *testing.T) {
	s, store, p, source, now := recoveryFixture(t)
	definition := RestoreSourceDefinition{Spec: source.Spec, BackendID: source.BackendID, BackendFingerprint: source.BackendFingerprint, ProviderResourceID: source.ProviderResourceID, DataResourceID: source.DataResourceID}
	store.mu.Lock()
	live := store.databases[source.ID]
	live.Spec.RestoreWindowSeconds = 10
	store.databases[source.ID] = live
	store.mu.Unlock()
	point := now.Add(-5 * time.Second)
	base := p.read
	p.read = func(d RestoreSourceDefinition) (RestoreSourceObservation, error) {
		*now = now.Add(9 * time.Second)
		return base(d)
	}
	_, created, err := s.RestoreWithResult(t.Context(), RestoreDatabaseRequest{AccountID: source.AccountID, SourceDatabaseID: source.ID, Name: "target", PointInTime: point, SourceDefinition: &definition})
	if !errors.Is(err, ErrInvalid) || created || p.restoreCalls != 0 {
		t.Fatal("metadata latency widened live retention", created, err, p.restoreCalls)
	}
	if _, err := store.FindByName(t.Context(), source.AccountID, "target"); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired point reserved target", err)
	}
}
