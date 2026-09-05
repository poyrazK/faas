from typing import Literal

UpsertDevSessionRequestRuntime = Literal["go124", "go124-alpine", "node22", "node24", "python312", "python313"]

UPSERT_DEV_SESSION_REQUEST_RUNTIME_VALUES: set[UpsertDevSessionRequestRuntime] = {
    "go124",
    "go124-alpine",
    "node22",
    "node24",
    "python312",
    "python313",
}


def check_upsert_dev_session_request_runtime(value: str) -> UpsertDevSessionRequestRuntime:
    if value in UPSERT_DEV_SESSION_REQUEST_RUNTIME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPSERT_DEV_SESSION_REQUEST_RUNTIME_VALUES!r}")
