from typing import Literal

ReleaseDiffFieldField = Literal[
    "command",
    "entrypoint",
    "env_override_keys",
    "handler",
    "healthcheck",
    "kind",
    "liveness_probe",
    "min_instances",
    "port",
    "readiness_probe",
    "runtime_base",
    "sidecars",
    "workflows",
]

RELEASE_DIFF_FIELD_FIELD_VALUES: set[ReleaseDiffFieldField] = {
    "command",
    "entrypoint",
    "env_override_keys",
    "handler",
    "healthcheck",
    "kind",
    "liveness_probe",
    "min_instances",
    "port",
    "readiness_probe",
    "runtime_base",
    "sidecars",
    "workflows",
}


def check_release_diff_field_field(value: str) -> ReleaseDiffFieldField:
    if value in RELEASE_DIFF_FIELD_FIELD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RELEASE_DIFF_FIELD_FIELD_VALUES!r}")
