from typing import Literal

SecretFindingSeverity = Literal["high", "medium"]

SECRET_FINDING_SEVERITY_VALUES: set[SecretFindingSeverity] = {
    "high",
    "medium",
}


def check_secret_finding_severity(value: str) -> SecretFindingSeverity:
    if value in SECRET_FINDING_SEVERITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SECRET_FINDING_SEVERITY_VALUES!r}")
