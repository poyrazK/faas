from typing import Literal

DebugDependencyImpactExemplarWindow = Literal["baseline", "current"]

DEBUG_DEPENDENCY_IMPACT_EXEMPLAR_WINDOW_VALUES: set[DebugDependencyImpactExemplarWindow] = {
    "baseline",
    "current",
}


def check_debug_dependency_impact_exemplar_window(value: str) -> DebugDependencyImpactExemplarWindow:
    if value in DEBUG_DEPENDENCY_IMPACT_EXEMPLAR_WINDOW_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_DEPENDENCY_IMPACT_EXEMPLAR_WINDOW_VALUES!r}")
