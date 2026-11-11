package rootfs

import "github.com/onebox-faas/faas/pkg/fnadapter"

// The function adapters live in pkg/fnadapter so `gregale dev --local` can
// run the same programs without importing image-building code. These names
// keep the image build and its tests unchanged.
var nodeFunctionAdapter = fnadapter.Node

const (
	pythonFunctionAdapter      = fnadapter.Python
	nodeFunctionBackground     = fnadapter.NodeBackground
	nodeAdapterHandlerImport   = fnadapter.NodeHandlerImport
	pythonAdapterHandlerImport = fnadapter.PythonHandlerImport
)

func nodeFunctionAdapterFor(handlerRel string) string   { return fnadapter.NodeFor(handlerRel) }
func pythonFunctionAdapterFor(handlerRel string) string { return fnadapter.PythonFor(handlerRel) }
