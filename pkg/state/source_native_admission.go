package state

// adr: 435. Distinct source producers require consumed-byte native capability.

func standardSourceNativeCapture(capture InstanceApplicationStandardAdmission) bool {
	for _, artifact := range capture.RuntimeArtifacts {
		if artifact.Kind == "source-app-layer" || artifact.Kind == "function-layer" {
			return true
		}
	}
	return false
}
