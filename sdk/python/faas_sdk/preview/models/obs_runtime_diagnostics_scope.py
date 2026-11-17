from typing import Literal

ObsRuntimeDiagnosticsScope = Literal["selected_runtime_diagnostics"]

OBS_RUNTIME_DIAGNOSTICS_SCOPE_VALUES: set[ObsRuntimeDiagnosticsScope] = {
    "selected_runtime_diagnostics",
}


def check_obs_runtime_diagnostics_scope(value: str) -> ObsRuntimeDiagnosticsScope:
    if value in OBS_RUNTIME_DIAGNOSTICS_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_DIAGNOSTICS_SCOPE_VALUES!r}")
