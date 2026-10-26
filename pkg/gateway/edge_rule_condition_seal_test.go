package gateway

import (
	"context"
	"reflect"
	"testing"
)

// Pinned requests match against the sealed JSON copy of a host policy. Every
// resolved kind has its own AppID, which hides the embedded condition's AppID
// from encoding/json, so on rc.296 every edge-rule hit and event carried app
// "" and each flush failed. The seal must keep the whole condition, for every
// kind.
func TestSealPolicyKeepsEdgeRuleConditionIdentity(t *testing.T) {
	entry := &HostEntry{Host: "app.example"}
	conditionType := reflect.TypeFor[EdgeRuleCondition]()
	conditionField := func(slice reflect.Value) (reflect.StructField, bool) {
		if slice.Kind() != reflect.Slice || slice.Type().Elem().Kind() != reflect.Struct {
			return reflect.StructField{}, false
		}
		field, ok := slice.Type().Elem().FieldByName("EdgeRuleCondition")
		return field, ok && field.Anonymous && field.Type == conditionType
	}
	want := map[string]EdgeRuleCondition{}
	value := reflect.ValueOf(entry).Elem()
	for i := range value.NumField() {
		slice := value.Field(i)
		field, ok := conditionField(slice)
		if !ok || !slice.CanSet() {
			continue
		}
		name := value.Type().Field(i).Name
		condition := EdgeRuleCondition{RuleID: name + "-rule", AppID: name + "-app", LogOnly: true}
		rule := reflect.New(slice.Type().Elem()).Elem()
		rule.FieldByIndex(field.Index).Set(reflect.ValueOf(condition))
		// The kind's own identity fields carry the same values, as the loader sets them.
		for outer, id := range map[string]string{"ID": condition.RuleID, "AppID": condition.AppID} {
			if f, ok := rule.Type().FieldByName(outer); ok && len(f.Index) == 1 && f.Type.Kind() == reflect.String {
				rule.Field(f.Index[0]).SetString(id)
			}
		}
		slice.Set(reflect.Append(slice, rule))
		want[name] = condition
	}
	if len(want) < 10 {
		t.Fatalf("found %d rule kinds with a condition; the reflection walk is broken", len(want))
	}
	if err := entry.SealPolicy(); err != nil {
		t.Fatal(err)
	}
	ctx, err := WithPinnedHostPolicy(context.Background(), entry.Host, entry)
	if err != nil {
		t.Fatal(err)
	}
	pinned, ok := PinnedHostPolicy(ctx, entry.Host)
	if !ok || pinned == entry {
		t.Fatal("no sealed copy pinned")
	}
	sealed := reflect.ValueOf(pinned).Elem()
	for name, condition := range want {
		slice := sealed.FieldByName(name)
		field, _ := conditionField(slice)
		got := slice.Index(0).FieldByIndex(field.Index).Interface().(EdgeRuleCondition)
		if got.RuleID != condition.RuleID || got.AppID != condition.AppID || got.LogOnly != condition.LogOnly {
			t.Errorf("%s: sealed condition = %+v, want %+v", name, got, condition)
		}
	}
}
