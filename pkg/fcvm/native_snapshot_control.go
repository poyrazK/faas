// adr: 568 — control adapters are fixed at native recovery startup.
package fcvm

import "context"

type nativeSnapshotControlBackend interface {
	Request(context.Context, string, nativeLaunchRecord, string, string, any) error
}
