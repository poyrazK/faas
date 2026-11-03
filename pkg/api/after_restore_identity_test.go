package api

import "testing"

// ADR-20261031193635 slice 5: after_restore.identity opts a branch into
// restoring from its parent's credential-free capture; only "reload" is valid.
func TestAfterRestoreIdentity(t *testing.T) {
	for _, tc := range []struct {
		identity string
		valid    bool
		reloads  bool
	}{
		{"", true, false},
		{AfterRestoreIdentityReload, true, true},
		{"keep", false, false},
	} {
		hook := &AfterRestoreHook{Path: "/internal/after-restore", Identity: tc.identity}
		if err := hook.Validate(); (err == nil) != tc.valid {
			t.Errorf("identity %q: Validate() = %v, want valid=%v", tc.identity, err, tc.valid)
		}
		if hook.ReloadsIdentity() != tc.reloads {
			t.Errorf("identity %q: ReloadsIdentity() = %v", tc.identity, !tc.reloads)
		}
	}
	var none *AfterRestoreHook
	if none.ReloadsIdentity() {
		t.Error("an absent hook reloads identity")
	}
}
