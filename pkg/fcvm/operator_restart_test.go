// adr: 1522
package fcvm

import "testing"

func TestOperatorRestartBindsPhysicalGeneration(t *testing.T) {
	vmm := &fakeVMM{}
	manager := newTestManager(&fakeRunner{}, vmm)
	const instance = "operator-restart-generation"
	if _, err := manager.ColdBoot(t.Context(), req(instance)); err != nil {
		t.Fatal(err)
	}
	generation, err := manager.OperatorRestartGeneration(instance)
	if err != nil {
		t.Fatal(err)
	}
	again, err := manager.OperatorRestartGeneration(instance)
	if err != nil || generation == "" || generation != again {
		t.Fatalf("unstable generation: %q %q %v", generation, again, err)
	}
	if err := manager.DestroyOperatorRestart(t.Context(), instance, "stale-generation"); err == nil {
		t.Fatal("stale generation destroyed VM")
	}
	if manager.LiveCount() != 1 {
		t.Fatal("stale generation changed live inventory")
	}
	if err := manager.DestroyOperatorRestart(t.Context(), instance, generation); err != nil {
		t.Fatal(err)
	}
	if manager.LiveCount() != 0 || manager.LeasedCount() != 0 {
		t.Fatal("teardown acknowledged before resources were released")
	}
	if _, err := manager.ColdBoot(t.Context(), req(instance)); err != nil {
		t.Fatal(err)
	}
	replacement, err := manager.OperatorRestartGeneration(instance)
	if err != nil || replacement == generation {
		t.Fatalf("replacement generation = %q, %v", replacement, err)
	}
	if err := manager.DestroyOperatorRestart(t.Context(), instance, generation); err == nil {
		t.Fatal("late restart destroyed replacement")
	}
	if manager.LiveCount() != 1 {
		t.Fatal("replacement disappeared")
	}
	if err := manager.Destroy(t.Context(), instance); err != nil {
		t.Fatal(err)
	}
}
