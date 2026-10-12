package main

import (
	"errors"
	"net/url"
	"strings"
	"sync"
)

// MCPResourceUpdateAdapter connects resource update publishers to active
// subscriptions. The default adapter is process-local; applications can set a
// shared adapter before serving requests to fan updates across replicas.
type MCPResourceUpdateAdapter interface {
	Subscribe(uri string, listener func()) func()
	Publish(uri string)
}

type localResourceUpdateHub struct {
	mu          sync.RWMutex
	nextID      uint64
	subscribers map[string]map[uint64]func()
}

func newLocalResourceUpdateHub() *localResourceUpdateHub {
	return &localResourceUpdateHub{subscribers: make(map[string]map[uint64]func())}
}

func (hub *localResourceUpdateHub) Subscribe(uri string, listener func()) func() {
	if !validResourceUpdateURI(uri) || listener == nil {
		return func() {}
	}
	hub.mu.Lock()
	hub.nextID++
	id := hub.nextID
	if hub.subscribers[uri] == nil {
		hub.subscribers[uri] = make(map[uint64]func())
	}
	hub.subscribers[uri][id] = listener
	hub.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			hub.mu.Lock()
			defer hub.mu.Unlock()
			delete(hub.subscribers[uri], id)
			if len(hub.subscribers[uri]) == 0 {
				delete(hub.subscribers, uri)
			}
		})
	}
}

func (hub *localResourceUpdateHub) Publish(uri string) {
	if !validResourceUpdateURI(uri) {
		return
	}
	hub.mu.RLock()
	listeners := make([]func(), 0, len(hub.subscribers[uri]))
	for _, listener := range hub.subscribers[uri] {
		listeners = append(listeners, listener)
	}
	hub.mu.RUnlock()
	for _, listener := range listeners {
		listener()
	}
}

var (
	resourceUpdateAdapterMu sync.RWMutex
	resourceUpdateAdapter   MCPResourceUpdateAdapter = newLocalResourceUpdateHub()
)

// SetMCPResourceUpdateAdapter installs an application-owned shared event
// adapter. Call it before serving requests. The adapter must support concurrent
// Subscribe, Publish and unsubscribe calls.
func SetMCPResourceUpdateAdapter(adapter MCPResourceUpdateAdapter) error {
	if adapter == nil {
		return errors.New("MCP resource update adapter is required")
	}
	resourceUpdateAdapterMu.Lock()
	resourceUpdateAdapter = adapter
	resourceUpdateAdapterMu.Unlock()
	return nil
}

// NotifyResourceUpdated publishes a change hint for an absolute resource URI.
// Subscribers still need to call resources/read to fetch the current content.
func NotifyResourceUpdated(uri string) error {
	if !validResourceUpdateURI(uri) {
		return errors.New("resource update URI must be an absolute URI under 16 KiB")
	}
	resourceUpdateAdapterMu.RLock()
	adapter := resourceUpdateAdapter
	resourceUpdateAdapterMu.RUnlock()
	adapter.Publish(uri)
	return nil
}

func subscribeResourceUpdate(uri string, listener func()) func() {
	resourceUpdateAdapterMu.RLock()
	adapter := resourceUpdateAdapter
	resourceUpdateAdapterMu.RUnlock()
	return adapter.Subscribe(uri, listener)
}

func validResourceUpdateURI(uri string) bool {
	if uri == "" || len(uri) > maxResourceURIBytes || strings.ContainsAny(uri, "\x00\r\n") {
		return false
	}
	parsed, err := url.Parse(uri)
	return err == nil && parsed.Scheme != ""
}
