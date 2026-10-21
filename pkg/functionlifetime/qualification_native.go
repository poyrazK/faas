//go:build function_background_native && metal

package functionlifetime

// NativeQualificationBuild is a dedicated acceptance build. Do not deploy it
// as production qualification; the native lifecycle suite must pass first.
const NativeQualificationBuild = true
