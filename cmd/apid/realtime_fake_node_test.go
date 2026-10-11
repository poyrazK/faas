package main

// fakeRealtimeNode is a counting realtime node operator shared by the realtime
// reconcile and route-metrics tests (copied from pkg/apid/managedrealtime's owner tests).

import (
	"context"

	"github.com/onebox-faas/faas/pkg/realtime"
)

type fakeRealtimeNode struct {
	connections      []realtime.ConnectionInfo
	endpoints        []string
	channelRoutes    []realtime.ChannelRoute
	channelRoutesErr error
	sends            int
	closes           int
	subs             int
	pubs             int
	registered       int
	removed          int
	connReads        int
	routeReads       int
	connErr          error
	publishErr       error
}

func (f *fakeRealtimeNode) Send(context.Context, string, string, realtime.Message) error {
	f.sends++
	return nil
}
func (f *fakeRealtimeNode) CloseConnection(context.Context, string, string, string) error {
	f.closes++
	return nil
}
func (f *fakeRealtimeNode) Subscribe(context.Context, string, string, string) error {
	f.subs++
	return nil
}
func (f *fakeRealtimeNode) Unsubscribe(context.Context, string, string, string) error {
	f.subs++
	return nil
}
func (f *fakeRealtimeNode) Publish(context.Context, string, string, realtime.Message) (int, error) {
	f.pubs++
	if f.publishErr != nil {
		return 0, f.publishErr
	}
	return 1, nil
}
func (f *fakeRealtimeNode) Connections(context.Context) ([]realtime.ConnectionInfo, error) {
	f.connReads++
	if f.connErr != nil {
		return nil, f.connErr
	}
	return append([]realtime.ConnectionInfo(nil), f.connections...), nil
}
func (f *fakeRealtimeNode) Endpoints(context.Context) ([]string, error) {
	if f.connErr != nil {
		return nil, f.connErr
	}
	return append([]string(nil), f.endpoints...), nil
}
func (f *fakeRealtimeNode) ChannelRoutes(context.Context) ([]realtime.ChannelRoute, error) {
	f.routeReads++
	if f.channelRoutesErr != nil {
		return nil, f.channelRoutesErr
	}
	if f.channelRoutes != nil {
		return append([]realtime.ChannelRoute(nil), f.channelRoutes...), nil
	}
	if f.connErr != nil {
		return nil, f.connErr
	}
	routes := make([]realtime.ChannelRoute, 0)
	for _, connection := range f.connections {
		for _, channel := range connection.Channels {
			routes = append(routes, realtime.ChannelRoute{EndpointID: connection.EndpointID, Channel: channel})
		}
	}
	return routes, nil
}
func (f *fakeRealtimeNode) RegisterEndpoint(context.Context, realtime.Endpoint) error {
	f.registered++
	return nil
}
func (f *fakeRealtimeNode) RemoveEndpoint(context.Context, string) error { f.removed++; return nil }
