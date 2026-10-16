//go:build !function_background_native || !metal

package functionlifetime

// NativeQualificationBuild enables a test-only build, never evidence that
// native acceptance passed. Ordinary production builds keep activation off.
const NativeQualificationBuild = false
