from typing import Literal

AccountHealthSignalKind = Literal["app_oom", "deploy_failing", "jobs_failing", "limit_pressure"]

ACCOUNT_HEALTH_SIGNAL_KIND_VALUES: set[AccountHealthSignalKind] = {
    "app_oom",
    "deploy_failing",
    "jobs_failing",
    "limit_pressure",
}


def check_account_health_signal_kind(value: str) -> AccountHealthSignalKind:
    if value in ACCOUNT_HEALTH_SIGNAL_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ACCOUNT_HEALTH_SIGNAL_KIND_VALUES!r}")
