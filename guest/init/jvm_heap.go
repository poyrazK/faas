package main

import "strings"

// jvmHeapDefault is the heap a JVM gets in a microVM when the workload sets
// none. A JVM sizes its default heap at 25% of the VM's memory, so a Spring
// Boot service in a 512 MB VM ran with a 128 MB heap and failed with
// OutOfMemoryError or crawled in GC while most of the VM stayed unused
// (retail-store feedback B3, 2026-11-17). The VM runs one workload, so 75%
// leaves room for metaspace, threads and the guest.
const jvmHeapDefault = "-XX:MaxRAMPercentage=75.0"

// jvmHeapOptOutEnv set to "off" keeps the JVM's own default.
const jvmHeapOptOutEnv = "GREGALE_JVM_HEAP_DEFAULT"

// jvmHeapFlags are the options that already size the heap; any of them in
// the workload's JVM options or argv leaves its choice alone.
var jvmHeapFlags = []string{"-Xmx", "-XX:MaxRAMPercentage", "-XX:MaxRAM=", "-XX:MaxRAMFraction", "-XX:MaxHeapSize"}

// jvmOptionEnvs are where images and start scripts commonly set JVM options.
var jvmOptionEnvs = []string{"JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "JAVA_OPTS", "JAVA_OPTIONS"}

// StampJVMHeapEnv adds jvmHeapDefault to JAVA_TOOL_OPTIONS for a workload
// whose image has a JVM and that sizes no heap itself.
func StampJVMHeapEnv(env []string, commands ...string) []string {
	return stampJVMHeapEnv(restoreReseedImageRoot, env, commands)
}

func stampJVMHeapEnv(root string, env []string, commands []string) []string {
	if strings.EqualFold(envValue(env, jvmHeapOptOutEnv), "off") || !imageHasJVM(root, env, commands) {
		return env
	}
	for _, key := range jvmOptionEnvs {
		if sizesHeap(envValue(env, key)) {
			return env
		}
	}
	if sizesHeap(strings.Join(commands, " ")) {
		return env
	}
	return setEnvValue(env, "JAVA_TOOL_OPTIONS", strings.TrimSpace(envValue(env, "JAVA_TOOL_OPTIONS")+" "+jvmHeapDefault))
}

func sizesHeap(options string) bool {
	for _, flag := range jvmHeapFlags {
		if strings.Contains(options, flag) {
			return true
		}
	}
	return false
}

func imageHasJVM(root string, env []string, commands []string) bool {
	for _, java := range javaCandidates(root, env, commands) {
		if _, ok := resolveInRoot(root, java); ok {
			return true
		}
	}
	return false
}
