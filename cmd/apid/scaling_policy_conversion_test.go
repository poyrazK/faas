package main

import (
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// The two conversions between api.ScalingPolicy and state.ScalingPolicy are
// hand-maintained field copies, because the types live in different packages
// and cannot be struct-converted. That makes them the fourth place in this
// codebase where a new field was silently dropped:
//
//   - ScalingPolicy.UnmarshalJSON dropped `targets` (fixed in #3208)
//   - OpsMetrics' struct literal dropped a gauge (fixed in #3223)
//   - policyShape dropped whatever was added after it (same fix as #3208)
//   - and these two, which dropped `targets`, `timezone`, `schedules` and
//     `target.name` for the entire life of ADR-194, ADR-195 and ADR-202
//
// The last one was the worst: PATCH /v1/apps/{slug} accepted a multi-signal
// policy, validated it, returned 200, and stored a policy with no targets.
// Every unit test built the state policy directly and the store conformance
// case calls UpdateApp, so both sides of this boundary were untested until an
// e2e drove a real apid.
//
// These tests are the guard. They use reflection over the struct fields
// rather than a hand-written list, so adding a field to either type without
// teaching both conversions fails here.

// TestPolicyRoundTrip_CarriesEveryField fills every settable field of the
// wire DTO with a distinctive value, converts to the state type and back,
// and requires the result to be identical.
//
// A zero would pass against a conversion that dropped the field, which is
// precisely the bug, so every value is non-zero.
func TestPolicyRoundTrip_CarriesEveryField(t *testing.T) {
	in := &api.ScalingPolicy{
		MinInstances: 2,
		MaxInstances: 9,
		Target:       &api.ScalingTarget{Metric: api.ScalingMetricRPS, Value: 12.5},
		Targets: []api.ScalingTarget{
			{Metric: api.ScalingMetricCPU, Value: 70},
			{Metric: api.ScalingMetricCustom, Name: "orders_pending", Value: 100},
		},
		ScaleOutCooldownS:       7,
		ScaleInCooldownS:        77,
		ConcurrencyOverflow:     api.ConcurrencyOverflowDrop,
		MaxQueueWaitMS:          1234,
		WakeMaxQueueDepth:       31,
		WakeMaxQueueWaitSeconds: 41,
		Timezone:                "Europe/Istanbul",
		Schedules: []api.ScalingSchedule{
			{Cron: "0 8 * * 1-5", DurationS: 43200, MinInstances: 3},
		},
	}

	statePolicy := policyPtrFromReq(&api.UpdateAppRequest{ScalingPolicy: in})
	if statePolicy == nil {
		t.Fatal("policyPtrFromReq returned nil for a non-nil policy")
	}
	got := statePolicyToDTO(statePolicy)
	if got == nil {
		t.Fatal("statePolicyToDTO returned nil")
	}

	if !reflect.DeepEqual(in, got) {
		t.Fatalf("a field did not survive api -> state -> api.\n in: %+v\nout: %+v\n\n"+
			"Both policyPtrFromReq (handlers_ext.go) and statePolicyToDTO (handlers.go) "+
			"must copy every field. This is how ADR-194's targets, ADR-195's schedules "+
			"and ADR-202's target.name were all accepted by PATCH and silently discarded.",
			in, got)
	}
}

// TestPolicyConversion_CoversEveryWireField is the structural half: it walks
// api.ScalingPolicy's fields by reflection, sets each one individually to a
// non-zero value, and requires it to survive the round trip on its own.
//
// The DeepEqual test above can be satisfied by a conversion that happens to
// copy the fields the fixture sets. This one cannot be satisfied by anything
// short of copying them all, and it names the offending field when it fails
// rather than printing two large structs.
func TestPolicyConversion_CoversEveryWireField(t *testing.T) {
	typ := reflect.TypeOf(api.ScalingPolicy{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue // unknownFields is decode bookkeeping, not policy
		}
		t.Run(field.Name, func(t *testing.T) {
			policy := &api.ScalingPolicy{}
			v := reflect.ValueOf(policy).Elem().Field(i)
			if !setDistinctiveValue(v) {
				t.Skipf("no distinctive value for %s (%s)", field.Name, field.Type)
			}
			got := statePolicyToDTO(policyPtrFromReq(&api.UpdateAppRequest{ScalingPolicy: policy}))
			if got == nil {
				t.Fatalf("conversion returned nil with only %s set", field.Name)
			}
			after := reflect.ValueOf(got).Elem().Field(i)
			if after.IsZero() {
				t.Fatalf("%s was dropped by the api -> state -> api conversion: set to %v, "+
					"came back zero. Copy it in BOTH policyPtrFromReq and statePolicyToDTO.",
					field.Name, v.Interface())
			}
		})
	}
}

// setDistinctiveValue puts a non-zero value into v, returning false for
// kinds the helper does not know how to populate. Slices and pointers are
// filled with one fully-populated element, since an empty slice or a
// zero-valued struct would not detect a dropped field.
func setDistinctiveValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Int:
		v.SetInt(7)
	case reflect.Float64:
		v.SetFloat(7)
	case reflect.String:
		// ConcurrencyOverflow is a closed set, so an arbitrary string
		// would be rejected elsewhere; "drop" is valid for it and
		// harmless as a timezone for a pure conversion test.
		v.SetString("drop")
	case reflect.Ptr:
		elem := reflect.New(v.Type().Elem())
		if !fillStruct(elem.Elem()) {
			return false
		}
		v.Set(elem)
	case reflect.Slice:
		elem := reflect.New(v.Type().Elem()).Elem()
		if !fillStruct(elem) {
			return false
		}
		v.Set(reflect.Append(v, elem))
	default:
		return false
	}
	return true
}

// fillStruct sets every exported field of a struct to a non-zero value so a
// conversion that copies the outer field but drops an inner one is caught
// too — which is exactly what happened to ScalingTarget.Name.
func fillStruct(v reflect.Value) bool {
	if v.Kind() != reflect.Struct {
		return false
	}
	for i := 0; i < v.NumField(); i++ {
		if !v.Type().Field(i).IsExported() {
			continue
		}
		f := v.Field(i)
		switch f.Kind() {
		case reflect.Int:
			f.SetInt(11)
		case reflect.Float64:
			f.SetFloat(11)
		case reflect.String:
			f.SetString("x")
		default:
			return false
		}
	}
	return true
}

// TestPolicyConversion_InnerTargetNameSurvives pins the specific inner-field
// case by name, because the reflection walk above only guarantees the OUTER
// field is non-zero — a conversion could copy Targets while dropping each
// element's Name and still pass it.
func TestPolicyConversion_InnerTargetNameSurvives(t *testing.T) {
	in := &api.ScalingPolicy{
		Target:  &api.ScalingTarget{Metric: api.ScalingMetricCustom, Name: "singular", Value: 1},
		Targets: []api.ScalingTarget{{Metric: api.ScalingMetricCustom, Name: "plural", Value: 2}},
	}
	got := statePolicyToDTO(policyPtrFromReq(&api.UpdateAppRequest{ScalingPolicy: in}))
	if got.Target == nil || got.Target.Name != "singular" {
		t.Errorf("singular target name = %+v, want \"singular\": the name selects WHICH "+
			"pushed metric the scheduler reads, so losing it disconnects the app from its signal",
			got.Target)
	}
	if len(got.Targets) != 1 || got.Targets[0].Name != "plural" {
		t.Errorf("targets[0] = %+v, want name \"plural\"", got.Targets)
	}
}

// TestPolicyConversion_ScheduleFieldsSurvive pins the schedule elements for
// the same reason.
func TestPolicyConversion_ScheduleFieldsSurvive(t *testing.T) {
	in := &api.ScalingPolicy{
		Timezone:  "Europe/Istanbul",
		Schedules: []api.ScalingSchedule{{Cron: "0 8 * * 1-5", DurationS: 43200, MinInstances: 3}},
	}
	statePolicy := policyPtrFromReq(&api.UpdateAppRequest{ScalingPolicy: in})
	// Assert through the helper the scheduler actually uses, not just the
	// struct: a schedule that survives as data but does not raise the floor
	// is still a broken feature.
	if statePolicy.Timezone != "Europe/Istanbul" {
		t.Errorf("timezone = %q, want Europe/Istanbul", statePolicy.Timezone)
	}
	if len(statePolicy.Schedules) != 1 {
		t.Fatalf("schedules = %d, want 1", len(statePolicy.Schedules))
	}
	if got := statePolicy.MaxReachableMinInstances(); got != 3 {
		t.Errorf("MaxReachableMinInstances = %d, want 3: the plan gate reads this, so a "+
			"dropped schedule would also bypass the min_instances entitlement", got)
	}
}
