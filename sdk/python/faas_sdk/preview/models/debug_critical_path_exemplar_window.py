from typing import Literal

DebugCriticalPathExemplarWindow = Literal["baseline", "current"]

DEBUG_CRITICAL_PATH_EXEMPLAR_WINDOW_VALUES: set[DebugCriticalPathExemplarWindow] = {
    "baseline",
    "current",
}


def check_debug_critical_path_exemplar_window(value: str) -> DebugCriticalPathExemplarWindow:
    if value in DEBUG_CRITICAL_PATH_EXEMPLAR_WINDOW_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_CRITICAL_PATH_EXEMPLAR_WINDOW_VALUES!r}")
