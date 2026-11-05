from typing import Literal

AppWorkerRecoveryScope = Literal["app"]

APP_WORKER_RECOVERY_SCOPE_VALUES: set[AppWorkerRecoveryScope] = {
    "app",
}


def check_app_worker_recovery_scope(value: str) -> AppWorkerRecoveryScope:
    if value in APP_WORKER_RECOVERY_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_WORKER_RECOVERY_SCOPE_VALUES!r}")
