package preflight

import "github.com/onebox-faas/faas/pkg/frameworkprofile"

// warningRule translates a frameworkprofile warning code into a preflight
// finding. Codes absent from this table fall through to amber: an inference
// warning we do not recognize is not evidence that the app is fine.
type warningRule struct {
	level  Level
	title  string
	remedy string
}

var warningRules = map[string]warningRule{
	"missing_start_command": {
		level:  LevelAmber,
		title:  "No start command found",
		remedy: "Add a `start` script to package.json, or set `start:` in gregale.yaml.",
	},
	"framework_not_detected": {
		level:  LevelAmber,
		title:  "No framework detected",
		remedy: "Add a Dockerfile, or declare `start:` and `port:` in gregale.yaml.",
	},
	"python_framework_not_detected": {
		level:  LevelAmber,
		title:  "Python found, but no supported framework",
		remedy: "Declare an explicit `start:` command in gregale.yaml.",
	},
	"entrypoint_guess": {
		level:  LevelAmber,
		title:  "Entrypoint was guessed",
		remedy: "Confirm the module path, or pin it with `start:` in gregale.yaml.",
	},
	"development_start_command": {
		level:  LevelAmber,
		title:  "Start command looks development-only",
		remedy: "Point `start` at a production server rather than a watch/dev process.",
	},
	"package_json_invalid": {
		level:  LevelAmber,
		title:  "package.json could not be parsed",
		remedy: "Fix the JSON so the start script and dependencies can be read.",
	},
	"loopback_bind_possible": {
		level:  LevelAmber,
		title:  "Source may bind to localhost",
		remedy: "Bind to 0.0.0.0 and read the port from $PORT, or traffic cannot reach the app.",
	},
	"profile_input_truncated": {
		level:  LevelAmber,
		title:  "Source too large to inspect fully",
		remedy: "The verdict covers only part of the tree; treat it as provisional.",
	},
	// Expected on every container deploy: the image, not the source, carries
	// the entrypoint. Informational, so it never downgrades an otherwise
	// clean container app.
	"container_command_deferred": {
		level:  LevelGreen,
		title:  "Entrypoint comes from the image",
		remedy: "",
	},
}

func findingForWarning(w frameworkprofile.Warning) Finding {
	rule, known := warningRules[w.Code]
	if !known {
		rule = warningRule{
			level:  LevelAmber,
			title:  "Needs review",
			remedy: "Check this against the container contract before deploying.",
		}
	}
	return Finding{
		Code:    w.Code,
		Level:   rule.level,
		Title:   rule.title,
		Detail:  w.Message,
		Remedy:  rule.remedy,
		Sources: w.Sources,
	}
}

// rank orders levels so a verdict can take the worst finding.
func rank(l Level) int {
	switch l {
	case LevelRed:
		return 2
	case LevelAmber:
		return 1
	default:
		return 0
	}
}

// worst returns whichever level is more severe.
func worst(a, b Level) Level {
	if rank(b) > rank(a) {
		return b
	}
	return a
}
