package runfiles

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// UploadContext bounds socket reads as well as storage work. Closing an HTTP
// body alone cannot interrupt a concurrent Read waiting for a slow peer.
func UploadContext(parent context.Context, w http.ResponseWriter) (context.Context, context.CancelFunc) {
	return transferContext(parent, w, true)
}

// DownloadContext interrupts both backend reads and a blocked response write.
func DownloadContext(parent context.Context, w http.ResponseWriter) (context.Context, context.CancelFunc) {
	return transferContext(parent, w, false)
}

func transferContext(parent context.Context, w http.ResponseWriter, upload bool) (context.Context, context.CancelFunc) {
	// Downloads share the bounded upload transfer budget.
	ctx, cancel := context.WithTimeout(parent, api.ExecutionFileUploadTimeout)
	controller := http.NewResponseController(w)
	setDeadline := controller.SetWriteDeadline
	if upload {
		setDeadline = controller.SetReadDeadline
	}
	deadline, _ := ctx.Deadline()
	_ = setDeadline(deadline)
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(finished)
		_ = setDeadline(time.Now())
	})
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			if !stop() {
				<-finished
			}
			cancel()
			_ = setDeadline(time.Time{})
		})
	}
}
