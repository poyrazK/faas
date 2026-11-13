package main

import "testing"

type DeploySourceFilesReport = deploySourceFilesReport
type DeploySourceFile = deploySourceFile

const SecretScanOff = modeOff

func ExplainDeploySourceForTest(t *testing.T, root, source string) (*deploySourceFilesReport, string, error) {
	t.Helper()
	report, archive, cleanup, err := prepareDeploySourceFiles(t.Context(), deploySourceFilesOptions{path: root, source: source, secretScan: "on", dryRun: true}, true)
	if err != nil {
		return nil, "", err
	}
	t.Cleanup(cleanup)
	return report, archive, report.finish(archive)
}
