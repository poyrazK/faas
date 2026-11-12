package main

var DevLocalInputsForTest = devLocalInputs

func LoadDevLocalEnvForTest(path string, base []string) ([]string, error) {
	return (devLocalEnvConfig{path: path, base: base}).load()
}
