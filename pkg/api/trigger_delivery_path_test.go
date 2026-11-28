package api

import "testing"

func TestIsTriggerDeliveryPath(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"/_triggers/kafka/abc", true},
		{"/_triggers", true},
		{"/_triggers/", true},
		{"/_TRIGGERS/kafka/abc", true},
		{"//_triggers/kafka/abc", true},
		{"/x/../_triggers/kafka/abc", true},
		{"/./_triggers/kafka", true},
		{"_triggers/kafka", true},
		{"/", false},
		{"", false},
		{"/_triggersx/kafka", false},
		{"/api/_triggers/kafka", false},
		{"/triggers/kafka", false},
	} {
		if got := IsTriggerDeliveryPath(tc.path); got != tc.want {
			t.Errorf("IsTriggerDeliveryPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
