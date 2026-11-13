// Package fnadapter holds the Node and Python function adapters: the
// generated programs that load a customer's handler(event, ctx) or Fetch API
// module and speak the runner's §4.9 envelope protocol. pkg/rootfs writes
// them into function images; `gregale dev --local` runs the same programs on
// a developer's machine (ADR-20261107210000). It has no platform
// dependencies beyond the constants the adapters embed.
package fnadapter

import (
	"strconv"
	"strings"
)

const (
	NodeHandlerImport   = `const handlerFile = path.join(path.dirname(process.argv[1]), "handler.js");`
	PythonHandlerImport = `handler_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), ".faas-handler.py")`
)

// NodeFor returns the Node adapter importing handlerRel, a
// slash path relative to the adapter's directory. The default handler.js
// yields the historical adapter byte for byte.
func NodeFor(handlerRel string) string {
	if handlerRel == "handler.js" {
		return Node
	}
	return strings.Replace(Node, NodeHandlerImport,
		"const handlerFile = path.join(path.dirname(process.argv[1]), "+strconv.Quote(handlerRel)+");", 1)
}

// PythonFor returns the Python adapter loading handlerRel in
// place and putting its directory on sys.path, so sibling imports in a
// shared source tree keep working.
func PythonFor(handlerRel string) string {
	return strings.Replace(Python, PythonHandlerImport,
		"handler_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "+strconv.Quote(handlerRel)+")\n"+
			"sys.path.insert(0, os.path.dirname(handler_path))", 1)
}
