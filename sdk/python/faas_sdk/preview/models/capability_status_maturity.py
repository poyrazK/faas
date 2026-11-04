from typing import Literal

CapabilityStatusMaturity = Literal["beta", "ga", "internal", "preview"]

CAPABILITY_STATUS_MATURITY_VALUES: set[CapabilityStatusMaturity] = {
    "beta",
    "ga",
    "internal",
    "preview",
}


def check_capability_status_maturity(value: str) -> CapabilityStatusMaturity:
    if value in CAPABILITY_STATUS_MATURITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CAPABILITY_STATUS_MATURITY_VALUES!r}")
