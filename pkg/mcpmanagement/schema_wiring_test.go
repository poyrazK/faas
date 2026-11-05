// adr: 20261103193000
package mcpmanagement

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"
)

func TestSchemaWiringPreservesToolContracts(t *testing.T) {
	for name := range toolsetOf {
		expected, err := json.Marshal(toolOutputSchema(name))
		if err != nil {
			t.Fatal(err)
		}
		actual, err := json.Marshal(registeredToolOutputSchema(name))
		if err != nil {
			t.Fatal(err)
		}
		var want, got any
		if json.Unmarshal(expected, &want) != nil || json.Unmarshal(actual, &got) != nil || !reflect.DeepEqual(want, got) {
			t.Fatalf("schema changed for %s", name)
		}
	}
}

func TestSchemaWiringIsStableAcrossConcurrentConnections(t *testing.T) {
	const name = "gregale_get_saved_plan"
	first := registeredToolOutputSchema(name)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if registeredToolOutputSchema(name) != first {
				t.Error("connection did not share the immutable schema")
			}
		})
	}
	wg.Wait()
	if registeredToolOutputSchema("gregale_get_plan") == first {
		t.Fatal("receipt and saved-review contracts were conflated")
	}
}
