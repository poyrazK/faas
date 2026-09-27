package realtime

import (
	"context"
	"sync"
	"testing"
)

func TestManagerPublishSelectsEndpointAndChannel(t *testing.T) {
	m := NewManager(Config{}, nil)
	defer m.cancel()
	first := Endpoint{ID: "first", MaxMessageBytes: 1024}
	second := Endpoint{ID: "second", MaxMessageBytes: 1024}
	for _, endpoint := range []Endpoint{first, second} {
		if err := m.RegisterEndpoint(endpoint); err != nil {
			t.Fatal(err)
		}
	}
	alerts := m.addConnection(first, "", nil)
	updates := m.addConnection(first, "", nil)
	otherEndpoint := m.addConnection(second, "", nil)
	for _, subscription := range []struct {
		id      string
		channel string
	}{
		{alerts.info.ID, "alerts"},
		{alerts.info.ID, "alerts"}, // A repeat subscription must not duplicate delivery.
		{updates.info.ID, "updates"},
		{otherEndpoint.info.ID, "alerts"},
	} {
		if err := m.Subscribe(subscription.id, subscription.channel); err != nil {
			t.Fatal(err)
		}
	}
	msg := Message{Data: []byte("hello")}
	if queued, err := m.Publish(context.Background(), first.ID, "alerts", msg); err != nil || queued != 1 {
		t.Fatalf("first alerts publish = (%d, %v), want (1, nil)", queued, err)
	}
	if got := string((<-alerts.outbound).Data); got != "hello" {
		t.Fatalf("first alerts payload = %q", got)
	}
	if len(updates.outbound) != 0 || len(otherEndpoint.outbound) != 0 {
		t.Fatal("publish crossed a channel or endpoint boundary")
	}
	if err := m.Unsubscribe(alerts.info.ID, "alerts"); err != nil {
		t.Fatal(err)
	}
	if queued, err := m.Publish(context.Background(), first.ID, "alerts", msg); err != nil || queued != 0 {
		t.Fatalf("unsubscribed publish = (%d, %v), want (0, nil)", queued, err)
	}
	m.RemoveEndpoint(first.ID)
	if queued, err := m.Publish(context.Background(), first.ID, "updates", msg); err != nil || queued != 1 {
		t.Fatalf("removed endpoint publish = (%d, %v), want (1, nil)", queued, err)
	}
	<-updates.outbound
	if queued, err := m.Publish(context.Background(), second.ID, "alerts", msg); err != nil || queued != 1 {
		t.Fatalf("second endpoint publish = (%d, %v), want (1, nil)", queued, err)
	}
	<-otherEndpoint.outbound
}

func TestManagerPublishConcurrentSubscriptionChanges(t *testing.T) {
	m := NewManager(Config{OutboundQueue: 512}, nil)
	defer m.cancel()
	endpoint := Endpoint{ID: "concurrent", MaxMessageBytes: 1024}
	if err := m.RegisterEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	c := m.addConnection(endpoint, "", nil)
	var workers sync.WaitGroup
	errors := make(chan error, 2)
	workers.Add(3)
	go func() {
		defer workers.Done()
		for i := 0; i < 100; i++ {
			if err := m.Subscribe(c.info.ID, "updates"); err != nil {
				errors <- err
				return
			}
			if err := m.Unsubscribe(c.info.ID, "updates"); err != nil {
				errors <- err
				return
			}
		}
	}()
	go func() {
		defer workers.Done()
		for i := 0; i < 100; i++ {
			if _, err := m.Publish(context.Background(), endpoint.ID, "updates", Message{Data: []byte("x")}); err != nil {
				errors <- err
				return
			}
		}
	}()
	go func() {
		defer workers.Done()
		for i := 0; i < 100; i++ {
			_ = m.Snapshot()
		}
	}()
	workers.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	if queued, err := m.Publish(context.Background(), endpoint.ID, "updates", Message{Data: []byte("x")}); err != nil || queued != 0 {
		t.Fatalf("publish after unsubscribe = (%d, %v), want (0, nil)", queued, err)
	}
}
