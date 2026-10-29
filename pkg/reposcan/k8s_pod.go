package reposcan

import (
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ociidentity"
)

// Pod-spec mapping behind the kubernetes-manifests capability
// (ADR-20261029093621 §3). The app's container sets the workload's size,
// healthcheck and process with the rules Compose already follows
// (ADR-20261023120000, ADR-683/684, ADR-1337); every pod-spec and container
// field that has no effect is reported with its reason.

const k8sDefaultContainerAnnotation = "kubectl.kubernetes.io/default-container"

const (
	k8sPlacementReason = "the scheduler places instances"
	k8sLivenessReason  = "an instance whose healthcheck fails after it was ready is replaced; a separate liveness check is not used, because a strict one is the usual cause of restart loops"
	k8sHTTPProbeReason = "HTTP and gRPC readiness from Kubernetes manifests are not applied yet; the platform waits for the serving port"
)

var k8sPodAppliedKeys = map[string]bool{
	"containers": true, "securityContext": true, "terminationGracePeriodSeconds": true,
}

var k8sPodHarmlessKeys = map[string]bool{
	"restartPolicy": true, "dnsPolicy": true, "enableServiceLinks": true,
	"automountServiceAccountToken": true, "schedulerName": true,
}

var k8sPodKeyReasons = map[string]string{
	"initContainers":            "init containers are not run; a one-time migration belongs in the release command",
	"volumes":                   composeVolumesReason,
	"nodeSelector":              k8sPlacementReason,
	"nodeName":                  k8sPlacementReason,
	"affinity":                  k8sPlacementReason,
	"tolerations":               k8sPlacementReason,
	"topologySpreadConstraints": k8sPlacementReason,
	"priorityClassName":         k8sPlacementReason,
	"hostNetwork":               composeKernelReason,
	"hostPID":                   composeKernelReason,
	"hostIPC":                   composeKernelReason,
	"shareProcessNamespace":     composeKernelReason,
	"runtimeClassName":          composeKernelReason,
	"dnsConfig":                 composeGuestNetReason,
	"hostAliases":               composeGuestNetReason,
	"hostname":                  composeGuestNetReason,
	"subdomain":                 composeGuestNetReason,
	"imagePullSecrets":          "registry credentials are set with `gregale registry`",
	"serviceAccountName":        k8sIdentityReason,
	"serviceAccount":            k8sIdentityReason,
}

var k8sContainerAppliedKeys = map[string]bool{
	"name": true, "image": true, "command": true, "args": true, "env": true, "ports": true,
	"workingDir": true, "resources": true, "readinessProbe": true, "startupProbe": true,
	"securityContext": true,
}

var k8sContainerHarmlessKeys = map[string]bool{
	"imagePullPolicy": true, "terminationMessagePath": true, "terminationMessagePolicy": true,
	"stdin": true, "stdinOnce": true, "tty": true,
}

var k8sContainerKeyReasons = map[string]string{
	"livenessProbe": k8sLivenessReason,
	"envFrom":       "ConfigMap and Secret values are not imported; set them with `gregale env set`",
	"volumeMounts":  composeVolumesReason,
	"lifecycle":     "lifecycle hooks are not run; terminationGracePeriodSeconds sets how long shutdown may take",
}

// k8sSecurityContextApplied are read into the process user; the harmless
// keys hold in a microVM anyway. Any other key is a kernel control.
var (
	k8sSecurityContextApplied  = map[string]bool{"runAsUser": true, "runAsGroup": true}
	k8sSecurityContextHarmless = map[string]bool{
		"runAsNonRoot": true, "readOnlyRootFilesystem": true,
		"allowPrivilegeEscalation": true, "seccompProfile": true,
	}
)

// applyK8sPodTemplate maps the pod template of a Deployment or CronJob onto
// its seed. The seed was built from the first container; when the
// default-container annotation names another one, that container is the app.
func applyK8sPodTemplate(s *workloadSeed, doc *yaml.Node, kind string, containers []k8sContainer) ([]string, error) {
	var obj map[string]any
	if err := doc.Decode(&obj); err != nil {
		return nil, err
	}
	templatePath := []string{"spec", "template"}
	if kind == "CronJob" {
		templatePath = []string{"spec", "jobTemplate", "spec", "template"}
	}
	template := k8sMap(obj, templatePath...)
	pod := k8sMap(template, "spec")
	annotations := k8sMap(template, "metadata", "annotations")
	rawContainers, _ := pod["containers"].([]any)

	index := 0
	if want, _ := annotations[k8sDefaultContainerAnnotation].(string); strings.TrimSpace(want) != "" {
		want = strings.TrimSpace(want)
		index = slices.IndexFunc(containers, func(c k8sContainer) bool { return c.Name == want })
		if index < 0 {
			return nil, fmt.Errorf("%s names container %q, which the pod does not have", k8sDefaultContainerAnnotation, want)
		}
	}
	app := containers[index]
	if strings.TrimSpace(app.Image) == "" {
		return nil, fmt.Errorf("container %q requires an image", app.Name)
	}
	if index != 0 {
		s.image, s.command, s.envKeys, s.ports = "", nil, nil, nil
		applyK8sContainer(s, app)
	}
	var container map[string]any
	if index < len(rawContainers) {
		container, _ = rawContainers[index].(map[string]any)
	}

	warnings := k8sPodKeyWarnings(pod, rawContainers, index)
	warnings = append(warnings, k8sContainerKeyWarnings(container)...)

	resources, resourceWarnings, err := k8sResources(container["resources"])
	if err != nil {
		return nil, err
	}
	s.resources = resources
	warnings = append(warnings, resourceWarnings...)

	healthcheck, probeWarnings, err := k8sHealthcheck(container)
	if err != nil {
		return nil, err
	}
	s.healthcheck = healthcheck
	warnings = append(warnings, probeWarnings...)

	process, processWarnings, err := k8sProcess(pod, container, app)
	if err != nil {
		return nil, err
	}
	s.imageProcess = process
	warnings = append(warnings, processWarnings...)
	// Kubernetes command replaces the image ENTRYPOINT and args its CMD,
	// so they are kept apart instead of joined into one command.
	if len(app.Command) > 0 {
		s.command = slices.Clone(app.Args)
	}
	return warnings, nil
}

func k8sPodKeyWarnings(pod map[string]any, containers []any, app int) []string {
	warnings := k8sKeyWarnings("", pod, k8sPodAppliedKeys, k8sPodHarmlessKeys, k8sPodKeyReasons)
	for i, raw := range containers {
		if i == app {
			continue
		}
		sidecar, _ := raw.(map[string]any)
		warnings = append(warnings, fmt.Sprintf("containers[%v] ignored: run sidecars as companions (docs/companions.md)", sidecar["name"]))
	}
	warnings = append(warnings, k8sSecurityContextWarnings("securityContext", k8sMap(pod, "securityContext"))...)
	return warnings
}

func k8sContainerKeyWarnings(container map[string]any) []string {
	warnings := k8sKeyWarnings("", container, k8sContainerAppliedKeys, k8sContainerHarmlessKeys, k8sContainerKeyReasons)
	return append(warnings, k8sSecurityContextWarnings("securityContext", k8sMap(container, "securityContext"))...)
}

func k8sSecurityContextWarnings(prefix string, ctx map[string]any) []string {
	var warnings []string
	for _, key := range slices.Sorted(maps.Keys(ctx)) {
		if !k8sSecurityContextApplied[key] && !k8sSecurityContextHarmless[key] {
			warnings = append(warnings, fmt.Sprintf("%s.%s ignored: %s", prefix, key, composeKernelReason))
		}
	}
	return warnings
}

func k8sKeyWarnings(prefix string, fields map[string]any, applied, harmless map[string]bool, reasons map[string]string) []string {
	var warnings []string
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		switch {
		case applied[key], harmless[key]:
		case reasons[key] != "":
			warnings = append(warnings, fmt.Sprintf("%s%s ignored: %s", prefix, key, reasons[key]))
		default:
			warnings = append(warnings, fmt.Sprintf("%s%s ignored: not supported by Gregale", prefix, key))
		}
	}
	return warnings
}

// k8sResources reads resources.limits, falling back to resources.requests:
// an app has one size, which is both its guarantee and its cap. Sizes go
// through Compose's rounding, and the plan check happens at admission
// (ADR-20261023120000).
func k8sResources(raw any) (*api.ComposeResources, []string, error) {
	resources, _ := raw.(map[string]any)
	limits, requests := k8sMap(resources, "limits"), k8sMap(resources, "requests")
	var warnings []string
	for _, key := range slices.Sorted(maps.Keys(resources)) {
		if key != "limits" && key != "requests" {
			warnings = append(warnings, "resources."+key+" ignored: only memory and cpu set an app's size")
		}
	}
	for _, group := range []struct {
		name   string
		values map[string]any
	}{{"limits", limits}, {"requests", requests}} {
		for _, key := range slices.Sorted(maps.Keys(group.values)) {
			if key != "memory" && key != "cpu" {
				warnings = append(warnings, fmt.Sprintf("resources.%s.%s ignored: only memory and cpu set an app's size", group.name, key))
			}
		}
	}
	out := &api.ComposeResources{}
	if value, key, ok := k8sLimitOrRequest(limits, requests, "memory"); ok {
		bytes, err := k8sQuantity(value)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", key, err)
		}
		mb, err := composeMemoryMB(int(math.Ceil(bytes)))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", key, err)
		}
		out.MemoryMB = mb
		if strings.Contains(key, "requests") {
			warnings = append(warnings, key+" used as the app's memory: an app has one size, both its guarantee and its cap")
		}
	}
	if value, key, ok := k8sLimitOrRequest(limits, requests, "cpu"); ok {
		cpus, err := k8sQuantity(value)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", key, err)
		}
		millicores, warning, err := composeCPUMillicores(cpus)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", key, err)
		}
		if warning != "" {
			warnings = append(warnings, key+" "+warning)
		}
		out.CPUMillicores = millicores
	}
	if out.IsZero() {
		return nil, warnings, nil
	}
	return out, warnings, nil
}

func k8sLimitOrRequest(limits, requests map[string]any, name string) (any, string, bool) {
	if value, ok := limits[name]; ok && value != nil {
		return value, "resources.limits." + name, true
	}
	if value, ok := requests[name]; ok && value != nil {
		return value, "resources.requests." + name, true
	}
	return nil, "", false
}

var k8sQuantityPattern = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)(Ki|Mi|Gi|Ti|Pi|Ei|n|u|m|k|M|G|T|P|E)?$`)

var k8sQuantityScale = map[string]float64{
	"": 1, "k": 1e3, "M": 1e6, "G": 1e9, "T": 1e12, "P": 1e15, "E": 1e18,
	"Ki": 1 << 10, "Mi": 1 << 20, "Gi": 1 << 30, "Ti": 1 << 40, "Pi": 1 << 50, "Ei": 1 << 60,
}

// k8sQuantityDivisor holds the fractional suffixes. Dividing keeps 300m
// exactly 0.3 where multiplying by 1e-3 would not.
var k8sQuantityDivisor = map[string]float64{"n": 1e9, "u": 1e6, "m": 1e3}

// k8sQuantity parses a Kubernetes resource quantity: decimal suffixes are
// powers of 1000, binary ones powers of 1024, and m is a thousandth
// (500m CPU is half a CPU).
func k8sQuantity(raw any) (float64, error) {
	var text string
	switch typed := raw.(type) {
	case int:
		return float64(typed), nil
	case float64:
		return typed, nil
	case string:
		text = strings.TrimSpace(typed)
	default:
		return 0, fmt.Errorf("%v must be a Kubernetes quantity such as 512Mi or 500m", raw)
	}
	match := k8sQuantityPattern.FindStringSubmatch(text)
	if match == nil {
		return 0, fmt.Errorf("%q must be a Kubernetes quantity such as 512Mi or 500m", text)
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil || math.IsInf(value, 0) {
		return 0, fmt.Errorf("%q must be a Kubernetes quantity such as 512Mi or 500m", text)
	}
	if divisor, ok := k8sQuantityDivisor[match[2]]; ok {
		return value / divisor, nil
	}
	return value * k8sQuantityScale[match[2]], nil
}

// Kubernetes probe defaults, applied so a probe means here what it meant in
// the cluster rather than inheriting the image's HEALTHCHECK timing.
const (
	k8sProbePeriodS  = 10
	k8sProbeTimeoutS = 1
	k8sProbeFailures = 3
)

// k8sHealthcheck turns the first exec probe of readinessProbe and
// startupProbe into the app's healthcheck, which gates readiness (ADR-683)
// and replaces an instance that fails after it was ready (ADR-684). A
// startupProbe's whole budget becomes the start period, during which
// failures do not count.
func k8sHealthcheck(container map[string]any) (*api.ComposeHealthcheck, []string, error) {
	var (
		check    *api.ComposeHealthcheck
		warnings []string
	)
	readiness, startup := k8sMap(container, "readinessProbe"), k8sMap(container, "startupProbe")
	for _, probe := range []struct {
		name   string
		fields map[string]any
	}{{"readinessProbe", readiness}, {"startupProbe", startup}} {
		if probe.fields == nil {
			continue
		}
		for _, handler := range []string{"httpGet", "grpc"} {
			if probe.fields[handler] != nil {
				warnings = append(warnings, fmt.Sprintf("%s.%s ignored: %s", probe.name, handler, k8sHTTPProbeReason))
			}
		}
		if probe.fields["tcpSocket"] != nil {
			warnings = append(warnings, probe.name+".tcpSocket ignored: the platform already waits for the serving port")
		}
		command, ok := k8sStrings(k8sMap(probe.fields, "exec")["command"])
		if !ok || check != nil {
			continue
		}
		timing, err := k8sProbeTiming(probe.name, probe.fields)
		if err != nil {
			return nil, nil, err
		}
		check = &api.ComposeHealthcheck{
			Test:          append([]string{"CMD"}, command...),
			IntervalNS:    int64(timing.period) * int64(time.Second),
			TimeoutNS:     int64(timing.timeout) * int64(time.Second),
			StartPeriodNS: int64(timing.delay) * int64(time.Second),
			Retries:       timing.failures,
		}
	}
	if check == nil {
		return nil, warnings, nil
	}
	if startup != nil {
		timing, err := k8sProbeTiming("startupProbe", startup)
		if err != nil {
			return nil, nil, err
		}
		budget := int64(timing.delay+timing.failures*timing.period) * int64(time.Second)
		check.StartPeriodNS = max(check.StartPeriodNS, budget)
	}
	if err := check.Validate(); err != nil {
		return nil, nil, err
	}
	return check, warnings, nil
}

type k8sTiming struct{ period, timeout, failures, delay int }

func k8sProbeTiming(name string, probe map[string]any) (k8sTiming, error) {
	var t k8sTiming
	for _, field := range []struct {
		key  string
		def  int
		min  int
		dest *int
	}{
		{"periodSeconds", k8sProbePeriodS, 1, &t.period},
		{"timeoutSeconds", k8sProbeTimeoutS, 1, &t.timeout},
		{"failureThreshold", k8sProbeFailures, 1, &t.failures},
		{"initialDelaySeconds", 0, 0, &t.delay},
	} {
		*field.dest = field.def
		raw, ok := probe[field.key]
		if !ok || raw == nil {
			continue
		}
		value, isInt := raw.(int)
		if !isInt || value < field.min {
			return k8sTiming{}, fmt.Errorf("%s.%s must be a whole number of at least %d", name, field.key, field.min)
		}
		*field.dest = value
	}
	return t, nil
}

// k8sProcess reads the entrypoint (command), working directory, user and
// shutdown grace into the ADR-1337 process contract.
func k8sProcess(pod, container map[string]any, app k8sContainer) (*api.ComposeProcess, []string, error) {
	process := &api.ComposeProcess{}
	var warnings []string
	if len(app.Command) > 0 {
		entrypoint := slices.Clone(app.Command)
		process.Entrypoint = &entrypoint
	}
	if dir, ok := container["workingDir"].(string); ok {
		process.WorkingDir = strings.TrimSpace(dir)
	}
	user, warning, err := k8sUser(k8sMap(pod, "securityContext"), k8sMap(container, "securityContext"))
	if err != nil {
		return nil, nil, err
	}
	process.User = user
	if warning != "" {
		warnings = append(warnings, warning)
	}
	if raw, ok := pod["terminationGracePeriodSeconds"]; ok && raw != nil {
		seconds, isInt := raw.(int)
		limit := int(api.MaxAppManifestStopGracePeriod / time.Second)
		switch {
		case !isInt || seconds < 0:
			return nil, nil, fmt.Errorf("terminationGracePeriodSeconds must be a whole number of seconds")
		case seconds < 1:
			warnings = append(warnings, fmt.Sprintf("terminationGracePeriodSeconds %d raised to 1: shutdown gets at least one second", seconds))
			seconds = 1
		case seconds > limit:
			warnings = append(warnings, fmt.Sprintf("terminationGracePeriodSeconds %d lowered to %d: shutdown may take at most %s", seconds, limit, api.MaxAppManifestStopGracePeriod))
			seconds = limit
		}
		process.StopGracePeriodS = &seconds
	}
	if err := process.Validate(); err != nil {
		return nil, nil, err
	}
	if process.IsZero() {
		return nil, warnings, nil
	}
	return process, warnings, nil
}

// k8sUser is the container's runAsUser and runAsGroup, each falling back to
// the pod's, as "uid" or "uid:gid".
func k8sUser(pod, container map[string]any) (string, string, error) {
	pick := func(key string) (any, bool) {
		if value, ok := container[key]; ok && value != nil {
			return value, true
		}
		value, ok := pod[key]
		return value, ok && value != nil
	}
	uid, hasUID := pick("runAsUser")
	gid, hasGID := pick("runAsGroup")
	if !hasUID {
		if hasGID {
			return "", "securityContext.runAsGroup ignored: a group applies only with runAsUser", nil
		}
		return "", "", nil
	}
	user := fmt.Sprint(uid)
	if hasGID {
		user += ":" + fmt.Sprint(gid)
	}
	if err := ociidentity.ValidateSpec(user); err != nil {
		return "", "", fmt.Errorf("securityContext.runAsUser/runAsGroup %q: %w", user, err)
	}
	return user, "", nil
}

func k8sMap(root map[string]any, path ...string) map[string]any {
	current := root
	for _, key := range path {
		next, ok := current[key].(map[string]any)
		if !ok {
			return nil
		}
		current = next
	}
	return current
}

func k8sStrings(raw any) ([]string, bool) {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		text, isString := item.(string)
		if !isString {
			return nil, false
		}
		out = append(out, text)
	}
	return out, true
}
