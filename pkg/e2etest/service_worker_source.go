package e2etest

import (
	"fmt"
)

// ManagedServiceWorkerSource supplies the exact static fixture handler to a
// real Dockerfile build. The host never runs a customer builder or substitutes
// a prebuilt rootfs for the reviewed source archive.
func ManagedServiceWorkerSource() (map[string][]byte, error) {
	server, err := helloServerBinary()
	if err != nil {
		return nil, fmt.Errorf("build source worker fixture: %w", err)
	}
	return map[string][]byte{
		"services/worker/hello-server": server,
		"services/worker/Dockerfile": []byte(`FROM scratch
COPY --chmod=755 hello-server /hello-server
EXPOSE 8080
ENTRYPOINT ["/hello-server"]
CMD ["-queue-ack", "-gitops-resilience", "-worker-startup-marker"]
`),
	}, nil
}
