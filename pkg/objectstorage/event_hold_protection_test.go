package objectstorage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 608
func TestEventHoldProtectionTransitions(t *testing.T) {
	day, year, shorter := int32(30), int32(1), int32(1)
	until := time.Now().UTC().AddDate(1, 0, 0).Truncate(time.Millisecond)
	earlier := until.Add(-time.Hour)
	active := api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Days: &day}, RetainUntilDate: &until}
	for _, tc := range []struct {
		name      string
		old, next api.ObjectVersionRetention
		allowed   bool
	}{
		{"enable", api.ObjectVersionRetention{}, api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Years: &year}}, true},
		{"release", active, api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "OFF"}, true},
		{"extend", active, api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Years: &year}}, true},
		{"duration_change_preserves_date", active, api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Days: &shorter}}, true},
		{"downgrade", active, api.ObjectVersionRetention{Mode: "GOVERNANCE", EventHold: "OFF"}, false},
		{"shorten_minimum", active, api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "OFF", RetainUntilDate: &earlier}, false},
		{"release_missing_on", api.ObjectVersionRetention{}, api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "OFF"}, false},
		{"fixed_minimum_missing", api.ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: &until}, api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Days: &day}}, false},
		{"fixed_minimum_preserved", api.ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: &until}, api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Days: &day}, RetainUntilDate: &until}, true},
		{"unknown_computed_date", api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Days: &day}}, api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "OFF"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if e := validateRetentionChange(tc.old, tc.next, time.Now()); (e == nil) != tc.allowed {
				t.Fatal(e)
			}
		})
	}
	desired := api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "OFF"}
	j := state.ObjectVersionProtection{ObjectVersionProtection: api.ObjectVersionProtection{Kind: "retention", Retention: &desired}, EventHoldBaseline: &active}
	observed := active.Clone()
	observed.EventHold = "OFF"
	if !eventHoldMatches(j, observed) {
		t.Fatal("computed release did not match")
	}
	observed.RetainUntilDate = &earlier
	if eventHoldMatches(j, observed) {
		t.Fatal("release shortened the observed bound")
	}
	observed.RetainUntilDate = nil
	if eventHoldMatches(j, observed) {
		t.Fatal("release without final date matched")
	}
	observed = active.Clone()
	observed.EventHold = "OFF"
	observed.EventHoldDuration = &api.ObjectRetentionPeriod{Years: &year}
	if eventHoldMatches(j, observed) {
		t.Fatal("release changed the observed duration")
	}
	j.EventHoldBaseline = nil
	if eventHoldMatches(j, observed) {
		t.Fatal("release without durable evidence matched")
	}
}

// adr: 608
func TestEventHoldProtectionRecoveryHTTP(t *testing.T) {
	for _, pg := range []bool{false, true} {
		for _, null := range []bool{false, true} {
			for _, operation := range []string{"enable", "duration", "release"} {
				t.Run(fmt.Sprintf("pg=%t/null=%t/%s", pg, null, operation), func(t *testing.T) {
					f := newLifecycleServiceFixture(t, pg)
					f.enableObjectLock(t)
					native := "private-event-version"
					selector := "null"
					if null {
						native = "null"
					} else {
						refs, e := f.st.RecordObjectVersions(t.Context(), f.bucket.AccountID, f.bucket.ID, []state.ObjectVersionIdentity{{Key: "key", ProviderVersionID: native}})
						if e != nil {
							t.Fatal(e)
						}
						selector = refs[0].ID
					}
					days := int32(30)
					until := time.Now().UTC().AddDate(0, 0, 30).Truncate(time.Millisecond)
					retention := api.ObjectVersionRetention{}
					desired := api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Days: &days}}
					if operation == "release" || operation == "duration" {
						retention = api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Days: &days}, RetainUntilDate: &until}
						desired = api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "OFF"}
						if operation == "duration" {
							shorter := int32(1)
							desired = api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Days: &shorter}}
						}
					}
					var mu sync.Mutex
					var puts, calls, metered atomic.Int32
					badRead := false
					p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						mu.Lock()
						defer mu.Unlock()
						q := r.URL.Query()
						w.Header().Set("Content-Type", "application/xml")
						switch {
						case q.Has("object-lock"):
							_, _ = io.WriteString(w, `<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`)
						case q.Has("versioning"):
							_, _ = io.WriteString(w, `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`)
						case q.Has("retention"):
							if q.Get("versionId") != native || r.Header.Get("X-Amz-Bypass-Governance-Retention") != "" {
								t.Error("wrong target or bypass", r.URL)
							}
							w.Header().Set("X-Amz-Version-Id", native)
							if r.Method == "PUT" {
								body, e := io.ReadAll(r.Body)
								if e != nil {
									t.Error(e)
								}
								v, e := DecodeObjectVersionRetention(body)
								if e != nil {
									t.Error(e)
								}
								if v.EventHold == "OFF" {
									retention.EventHold = "OFF"
								} else {
									retention = v
									retention.RetainUntilDate = &until
								}
								puts.Add(1)
								w.WriteHeader(500)
								_, _ = io.WriteString(w, `<Error><Code>InternalError</Code></Error>`)
								return
							}
							if badRead {
								_, _ = io.WriteString(w, `<Retention><Mode>COMPLIANCE</Mode><EventHold>OFF</EventHold></Retention>`)
								return
							}
							_, _ = io.WriteString(w, "<Retention>")
							if !retention.Empty() {
								_, _ = fmt.Fprintf(w, "<Mode>%s</Mode><EventHold>%s</EventHold><RetainUntilDate>%s</RetainUntilDate>", retention.Mode, retention.EventHold, retention.RetainUntilDate.Format(time.RFC3339Nano))
								if retention.EventHoldDuration != nil {
									_, _ = fmt.Fprintf(w, "<EventHoldDuration><Days>%d</Days></EventHoldDuration>", *retention.EventHoldDuration.Days)
								}
							}
							_, _ = io.WriteString(w, "</Retention>")
						default:
							t.Error("unexpected request", r.Method, r.URL)
							w.WriteHeader(500)
						}
					})).(Provider)
					ops := f.st.(state.ObjectVersionProtectionStore)
					svc := VersionProtectionService{Store: ops, References: f.st, BucketLock: f.st.(state.ObjectBucketObjectLockStore), Provider: p, BeforeRequest: func(context.Context) error { metered.Add(1); return nil }}
					input := state.ObjectVersionProtection{ObjectVersionProtection: api.ObjectVersionProtection{ID: uuid.NewString(), Key: "key", VersionID: selector, Kind: "retention", Retention: &desired}}
					if _, e := svc.Request(t.Context(), f.bucket, input); !errors.Is(e, ErrUnsupported) {
						t.Fatal("unenrolled admission", e)
					}
					svc.EventHolds = true
					j, e := svc.Request(t.Context(), f.bucket, input)
					if e != nil {
						t.Fatal(e)
					}
					svc.EventHolds = false // accepted intent survives enrollment removal before dispatch
					j, e = svc.Reconcile(t.Context(), f.bucket, j.ID)
					if e == nil || j.State != "waiting" || !j.Dispatched || j.EventHoldBaseline == nil || puts.Load() != 1 {
						t.Fatal("lost ACK discarded custody", j, e, puts.Load())
					}
					raw, _ := json.Marshal(state.ViewObjectVersionProtection(j))
					if !null && strings.Contains(string(raw), native) || strings.Contains(string(raw), "baseline") {
						t.Fatal("private evidence leaked", string(raw))
					}
					if f.pool != nil {
						for _, query := range []string{`UPDATE object_version_protection SET event_hold_baseline=NULL WHERE id=$1`, `UPDATE object_version_protection SET event_hold_baseline='{"mode":"COMPLIANCE","event_hold":"ON","event_hold_duration":{"days":1},"retain_until_date":"2030-01-01T00:00:00Z"}' WHERE id=$1`, `UPDATE object_version_protection SET dispatched=false WHERE id=$1`} {
							if _, e = f.pool.Exec(t.Context(), query, j.ID); e == nil {
								t.Fatal("raw SQL discarded evidence", query)
							}
						}
						for _, name := range []string{"20261004193552286_object_version_protection.sql", "20261005162807808_object_event_hold_protection.sql"} {
							data, e := migrations.FS.ReadFile(name)
							if e != nil {
								t.Fatal(e)
							}
							if _, e = f.pool.Exec(t.Context(), strings.SplitN(string(data), "-- +goose Down", 2)[0]); e != nil {
								t.Fatal("migration replay", e)
							}
						}
						data, _ := migrations.FS.ReadFile("20261005162807808_object_event_hold_protection.sql")
						if _, e = f.pool.Exec(t.Context(), strings.SplitN(string(data), "-- +goose Down", 2)[1]); e == nil {
							t.Fatal("rollback discarded custody")
						}
					}
					advance := func() {
						if f.pool != nil {
							if _, e := f.pool.Exec(t.Context(), `UPDATE object_version_protection SET retry_at=clock_timestamp(),lease_until=CASE WHEN lease_until IS NULL THEN NULL ELSE clock_timestamp()-interval '1 second' END`); e != nil {
								t.Fatal(e)
							}
						} else {
							f.expire(j.ID)
						}
					}
					svc.Store = f.reopen().(state.ObjectVersionProtectionStore)
					svc.EventHolds = false
					mu.Lock()
					badRead = true
					mu.Unlock()
					advance()
					j, e = svc.Reconcile(t.Context(), f.bucket, j.ID)
					if e == nil || j.State != "waiting" || puts.Load() != 1 {
						t.Fatal("incomplete readback settled or repeated mutation", j, e)
					}
					mu.Lock()
					badRead = false
					mu.Unlock()
					advance()
					j, e = svc.Reconcile(t.Context(), f.bucket, j.ID)
					if e != nil || j.State != "ready" || puts.Load() != 1 {
						t.Fatal("disabled recovery repeated mutation", j, e)
					}
					replay, e := svc.Request(t.Context(), f.bucket, input)
					if e != nil || replay.ID != j.ID || replay.State != "ready" {
						t.Fatal("stable retry lost receipt", replay, e)
					}
					if metered.Load() != calls.Load() {
						t.Fatal("unmetered requests", metered.Load(), calls.Load())
					}
				})
			}
		}
	}
}

// adr: 608
func TestEventHoldProtectionJournalGuards(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(fmt.Sprint("pg=", pg), func(t *testing.T) {
			f := newLifecycleServiceFixture(t, pg)
			f.enableObjectLock(t)
			refs, e := f.st.RecordObjectVersions(t.Context(), f.bucket.AccountID, f.bucket.ID, []state.ObjectVersionIdentity{{Key: "key", ProviderVersionID: "private-event"}})
			if e != nil {
				t.Fatal(e)
			}
			desired := api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "OFF"}
			ops := f.st.(state.ObjectVersionProtectionStore)
			j, e := ops.BeginObjectVersionProtection(t.Context(), state.ObjectVersionProtection{ObjectVersionProtection: api.ObjectVersionProtection{ID: uuid.NewString(), BucketID: f.bucket.ID, Key: "key", VersionID: refs[0].ID, Kind: "retention", Retention: &desired}, AccountID: f.bucket.AccountID, AppID: f.bucket.AppID})
			if e != nil {
				t.Fatal(e)
			}
			j, e = ops.ClaimObjectVersionProtection(t.Context(), j.ID, "guard")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = ops.DispatchObjectVersionProtection(t.Context(), j.ID, j.Token); !errors.Is(e, state.ErrConflict) {
				t.Fatal("legacy dispatch without baseline", e)
			}
			if _, e = ops.FinishObjectVersionProtection(t.Context(), j.ID, j.Token, "ready", ""); !errors.Is(e, state.ErrConflict) {
				t.Fatal("legacy finish without baseline", e)
			}
			if f.pool != nil {
				for _, query := range []string{`UPDATE object_version_protection SET dispatched=true WHERE id=$1`, `UPDATE object_version_protection SET state='ready',lease_token='',lease_until=NULL WHERE id=$1`, `UPDATE object_version_protection SET event_hold_baseline='{}' WHERE id=$1`} {
					if _, e = f.pool.Exec(t.Context(), query, j.ID); e == nil {
						t.Fatal("legacy SQL bypass", query)
					}
				}
			}
			days := int32(30)
			until := time.Now().UTC().AddDate(0, 0, 30)
			baseline := api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Days: &days}, RetainUntilDate: &until}
			prepared := f.st.(state.ObjectEventHoldProtectionStore)
			j, e = prepared.PrepareObjectEventHoldProtection(t.Context(), j.ID, j.Token, baseline)
			if e != nil {
				t.Fatal(e)
			}
			*j.EventHoldBaseline.EventHoldDuration.Days = 1
			saved, e := ops.GetObjectVersionProtection(t.Context(), f.bucket.AccountID, f.bucket.ID, j.ID)
			if e != nil || *saved.EventHoldBaseline.EventHoldDuration.Days != 30 {
				t.Fatal("baseline alias", saved, e)
			}
			changed := baseline.Clone()
			*changed.EventHoldDuration.Days = 1
			if _, e = prepared.PrepareObjectEventHoldProtection(t.Context(), j.ID, j.Token, changed); !errors.Is(e, state.ErrConflict) {
				t.Fatal("changed baseline", e)
			}
			if _, e = prepared.PrepareObjectEventHoldProtection(t.Context(), j.ID, "stale", baseline); !errors.Is(e, state.ErrConflict) {
				t.Fatal("stale preparation", e)
			}
			j, e = ops.DispatchObjectVersionProtection(t.Context(), j.ID, j.Token)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = prepared.PrepareObjectEventHoldProtection(t.Context(), j.ID, j.Token, baseline); !errors.Is(e, state.ErrConflict) {
				t.Fatal("post dispatch preparation", e)
			}
		})
	}
}
