package projectselection_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/projectselection"
)

func TestProtectedSelectionBinding(t *testing.T) {
	reviewed := projectselection.New(map[string]bool{" Alpha ": true, "ignored": false}, map[string]bool{"beta": true}, []string{"shop-gamma", "SHOP-GAMMA"})
	same := projectselection.New(map[string]bool{"alpha": true}, map[string]bool{"Beta": true}, []string{"shop-gamma"})
	if !projectselection.Matches(reviewed, same) {
		t.Fatal("equivalent filters do not match")
	}
	for _, changed := range []*projectselection.Filters{
		nil,
		projectselection.New(nil, nil, nil),
		projectselection.New(map[string]bool{"delta": true}, map[string]bool{"beta": true}, []string{"shop-gamma"}),
		projectselection.New(map[string]bool{"alpha": true}, nil, []string{"shop-gamma"}),
		projectselection.New(map[string]bool{"alpha": true}, map[string]bool{"beta": true}, nil),
	} {
		if projectselection.Matches(reviewed, changed) {
			t.Fatalf("changed filters accepted: %+v", changed)
		}
	}
	if projectselection.Matches(nil, projectselection.New(nil, nil, nil)) {
		t.Fatal("legacy unbound plan accepted")
	}
}
