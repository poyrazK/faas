package main

// AppScalePlanWarnings exposes the scale preview's warnings to the external
// test package (app_scale_plan_request_concurrency_test.go,
// commands_app_scale_plan_test.go), which referenced it without an export.
var AppScalePlanWarnings = appScalePlanWarnings
