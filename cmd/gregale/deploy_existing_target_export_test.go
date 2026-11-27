package main

// DeployTargetSummary exposes the preview's Target line.
func DeployTargetSummary(exists, linked bool) string { return deployTargetSummary(exists, linked) }

// SourceLinkedTo exposes the directory-to-app link check.
func SourceLinkedTo(dir, slug string) bool { return sourceLinkedTo(dir, slug) }

// UnlinkedExistingAppNotice exposes the deploy notice.
func UnlinkedExistingAppNotice(slug string) string { return unlinkedExistingAppNotice(slug) }

// GuardUnlinkedExistingApp exposes the unlinked-existing-app deploy guard.
var GuardUnlinkedExistingApp = guardUnlinkedExistingApp
