package e2etest

import "strconv"

// TCPEchoImageAboveBase is HelloImageAboveBase whose hello-server also runs a
// raw TCP echo listener on port (ADR-576): every chunk it reads comes back
// prefixed with "tcp-echo:". Every hello-server answers GET /dial, so a plain
// HelloImageAboveBase app is the matching caller. Neither needs a build.
func TCPEchoImageAboveBase(repo, helloBody string, port int) (fakeImage, string) {
	return layeredHelloImageOnPortWithCmd(repo, helloBody, true, 8080, []string{"/hello-server", "-tcp-echo", strconv.Itoa(port)})
}
