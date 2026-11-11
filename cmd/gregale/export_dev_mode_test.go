package main

type DevModeSelection = devModeSelection

func ResolveDevModeForTest(root string, function bool, explicit map[string]bool, watch bool, command string) (devModeSelection, error) {
	manifest, err := loadDevManifest(root)
	if err != nil {
		return devModeSelection{}, err
	}
	config := devSourceConfig{shape: shapeApp}
	if function {
		config.shape = shapeFunction
	}
	return resolveDevMode(root, config, manifest, explicit, watch, command)
}

var UpsertDevSessionWithModeForTest = upsertDevSessionWithMode
var ReportDevModeForTest = reportDevMode

func DevAllWatchArgsForTest(root string, watch *bool, stop bool) []string {
	opts := devAllOptions{stop: stop, watchSet: watch != nil}
	if watch != nil {
		opts.watch = *watch
	}
	return opts.childArgs(devAllApp{SourceDir: root, Project: "demo"}, false)
}
