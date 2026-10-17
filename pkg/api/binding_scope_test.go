package api

import "testing"

func TestQueueBindingScopePreservesSharedAndNamedAppEnvironment(t *testing.T) {
	shared, app, staging := "", "app", "staging"
	for _, tc := range []struct {
		name, label string
		environment *string
		selected    string
		want        bool
	}{
		{"shared", "app", &shared, "production", true},
		{"old shared", "app", nil, "production", true},
		{"named app excluded", "app", &app, "production", false},
		{"named app included", "app", &app, "app", true},
		{"staging excluded", staging, &staging, "production", false},
		{"staging included", staging, &staging, staging, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := QueueBindingInScope(AppBindingInventoryItem{Type: BindingTypeQueue, Scope: tc.label, QueueEnvironment: tc.environment}, tc.selected); got != tc.want {
				t.Fatalf("included=%t, want %t", got, tc.want)
			}
		})
	}
}
