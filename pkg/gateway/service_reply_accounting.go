package gateway

import "context"

type serviceReplyAccountingKey struct{}

// withServiceReplyAccounting marks an upgraded service call: the raw bridge
// records the bytes the called instance streams back as service reply bytes,
// internal egress meterd can leave out of its bill (billing review R8-2).
func withServiceReplyAccounting(ctx context.Context) context.Context {
	return context.WithValue(ctx, serviceReplyAccountingKey{}, true)
}

func isServiceReplyAccounting(ctx context.Context) bool {
	on, _ := ctx.Value(serviceReplyAccountingKey{}).(bool)
	return on
}
