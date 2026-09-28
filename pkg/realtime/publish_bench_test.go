package realtime

import (
	"context"
	"testing"
)

// BenchmarkManagerPublishSparseSubscribers measures the cost of publishing to a
// small channel on a busy node. Synthetic connections keep socket I/O out of
// the result while exercising the same subscription and publish paths.
func BenchmarkManagerPublishSparseSubscribers(b *testing.B) {
	const connections = 10_000
	const subscribers = 100
	m := NewManager(Config{}, nil)
	b.Cleanup(m.cancel)
	endpoint := Endpoint{ID: "benchmark", MaxMessageBytes: 1024}
	if err := m.RegisterEndpoint(endpoint); err != nil {
		b.Fatal(err)
	}
	receivers := make([]*connection, 0, subscribers)
	for i := 0; i < connections; i++ {
		c := addSyntheticConnection(b, m, endpoint.ID)
		if i < subscribers {
			if err := m.Subscribe(c.info.ID, "updates"); err != nil {
				b.Fatal(err)
			}
			receivers = append(receivers, c)
		}
	}
	msg := Message{Data: []byte("update")}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		queued, err := m.Publish(context.Background(), endpoint.ID, "updates", msg)
		if err != nil || queued != subscribers {
			b.Fatalf("publish = (%d, %v), want (%d, nil)", queued, err, subscribers)
		}
		for _, c := range receivers {
			<-c.outbound
		}
	}
}
