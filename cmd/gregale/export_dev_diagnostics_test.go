package main

import (
	"io"
	"sync"
)

type DevDiagnostic = devDiagnostic

var NewDevDiagnosticReporterForTest = newDevDiagnosticReporter
var StartDevRuntimeLogsForTest = startDevRuntimeLogs

func NewDevDiagnosticTaggerForTest(project, path string, out, errOut io.Writer) io.WriteCloser {
	return newDevJSONTagger(devAllApp{Project: project, RootDir: path}, &sync.Mutex{}, out, errOut)
}
