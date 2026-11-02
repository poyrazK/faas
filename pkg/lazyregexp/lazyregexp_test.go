package lazyregexp

import (
	"sync"
	"testing"
)

func TestCompilesOnFirstUseOutsideTests(t *testing.T) {
	saved := inTest
	inTest = false
	t.Cleanup(func() { inTest = saved })

	r := New(`^[a-z]+-(?P<n>[0-9]+)$`)
	if r.rx != nil {
		t.Fatal("New compiled the pattern before its first use")
	}
	if !r.MatchString("app-42") || r.MatchString("App-42") {
		t.Fatal("MatchString disagrees with the pattern")
	}
	if got := r.FindStringSubmatch("app-42"); len(got) != 2 || got[1] != "42" {
		t.Fatalf("FindStringSubmatch = %q", got)
	}
	if got := r.SubexpIndex("n"); got != 1 {
		t.Fatalf("SubexpIndex = %d, want 1", got)
	}
	if r.String() != `^[a-z]+-(?P<n>[0-9]+)$` {
		t.Fatalf("String = %q", r.String())
	}
}

func TestCompilesEagerlyInTests(t *testing.T) {
	if !inTest {
		t.Fatal("a go test binary is not detected as a test")
	}
	if New(`a+`).rx == nil {
		t.Fatal("New did not compile under go test")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("an invalid pattern did not panic at New under go test")
		}
	}()
	New(`(`)
}

func TestInvalidPatternPanicsOnFirstUse(t *testing.T) {
	saved := inTest
	inTest = false
	t.Cleanup(func() { inTest = saved })

	r := New(`(`)
	defer func() {
		if recover() == nil {
			t.Fatal("an invalid pattern did not panic on first use")
		}
	}()
	r.MatchString("x")
}

func TestConcurrentFirstUse(t *testing.T) {
	saved := inTest
	inTest = false
	t.Cleanup(func() { inTest = saved })

	r := New(`^x+$`)
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !r.MatchString("xxx") {
				t.Error("MatchString = false")
			}
		}()
	}
	wg.Wait()
}
