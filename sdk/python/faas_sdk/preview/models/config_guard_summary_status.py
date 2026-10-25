from typing import Literal

ConfigGuardSummaryStatus = Literal["clean", "no_traffic", "regressed", "restored", "superseded", "watching"]

CONFIG_GUARD_SUMMARY_STATUS_VALUES: set[ConfigGuardSummaryStatus] = {
    "clean",
    "no_traffic",
    "regressed",
    "restored",
    "superseded",
    "watching",
}


def check_config_guard_summary_status(value: str) -> ConfigGuardSummaryStatus:
    if value in CONFIG_GUARD_SUMMARY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CONFIG_GUARD_SUMMARY_STATUS_VALUES!r}")
