package state

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestRealtimeEventSchemaCache(t *testing.T) {
	object := json.RawMessage(`{"type":"object","required":["id"]}`)
	tests := []struct {
		name  string
		check func(t *testing.T, c *realtimeEventSchemaCache)
	}{
		{"compiles a schema once", func(t *testing.T, c *realtimeEventSchemaCache) {
			first, err := c.schema(object)
			if err != nil {
				t.Fatal(err)
			}
			again, err := c.schema(append(json.RawMessage(nil), object...))
			if err != nil || again != first {
				t.Fatalf("second lookup = %p, %v; want the cached %p", again, err, first)
			}
			other, err := c.schema(json.RawMessage(`{"type":"string"}`))
			if err != nil || other == first {
				t.Fatalf("different text = %p, %v; want its own schema", other, err)
			}
		}},
		{"does not keep a schema that fails to compile", func(t *testing.T, c *realtimeEventSchemaCache) {
			if _, err := c.schema(json.RawMessage(`{"type":`)); err == nil {
				t.Fatal("malformed schema compiled")
			}
			if len(c.compiled) != 0 {
				t.Fatalf("cache holds %d entries after a failed compile", len(c.compiled))
			}
		}},
		{"stays within its bound", func(t *testing.T, c *realtimeEventSchemaCache) {
			for i := range realtimeEventSchemaCacheMax + 10 {
				if _, err := c.schema(json.RawMessage(fmt.Sprintf(`{"type":"object","title":"t%d"}`, i))); err != nil {
					t.Fatal(err)
				}
			}
			if len(c.compiled) != realtimeEventSchemaCacheMax {
				t.Fatalf("cache holds %d entries, want %d", len(c.compiled), realtimeEventSchemaCacheMax)
			}
		}},
		{"validates through the cache", func(t *testing.T, c *realtimeEventSchemaCache) {
			if err := validateTypedRealtimeEvent(c, object, []byte(`{"id":1}`), false); err != nil {
				t.Fatalf("valid event refused: %v", err)
			}
			if err := validateTypedRealtimeEvent(c, object, []byte(`{}`), false); err == nil {
				t.Fatal("event without id accepted")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.check(t, newRealtimeEventSchemaCache()) })
	}
	t.Run("a nil cache compiles every time", func(t *testing.T) {
		var c *realtimeEventSchemaCache
		if err := validateTypedRealtimeEvent(c, object, []byte(`{"id":1}`), false); err != nil {
			t.Fatalf("valid event refused: %v", err)
		}
	})
}
