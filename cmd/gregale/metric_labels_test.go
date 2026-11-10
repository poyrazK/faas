package main_test

import (
	"maps"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// adr: 20261108160000 slice C2 — --metric-label key=value selects one series.
func TestMetricLabelFlags(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   map[string]string
		ok     bool
	}{
		{"none is the aggregate", nil, nil, true},
		{"two labels", []string{"region=eu", "tier=gold"}, map[string]string{"region": "eu", "tier": "gold"}, true},
		{"value may hold =", []string{"query=a=b"}, map[string]string{"query": "a=b"}, true},
		{"missing =", []string{"region"}, nil, false},
		{"duplicate key", []string{"region=eu", "region=us"}, nil, false},
		{"bad key", []string{"Region=eu"}, nil, false},
		{"too many", []string{"a=1", "b=2", "c=3", "d=4"}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := main.MetricLabelFlags(tt.values)
			if (err == nil) != tt.ok || !maps.Equal(got, tt.want) {
				t.Fatalf("got %v, %v; want %v ok=%v", got, err, tt.want, tt.ok)
			}
		})
	}
}
