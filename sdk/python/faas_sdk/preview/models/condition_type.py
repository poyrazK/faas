from typing import Literal

ConditionType = Literal[
    "CertificateReady", "Degraded", "LastRunSucceeded", "Progressing", "Ready", "RolledOut", "Succeeded", "Verified"
]

CONDITION_TYPE_VALUES: set[ConditionType] = {
    "CertificateReady",
    "Degraded",
    "LastRunSucceeded",
    "Progressing",
    "Ready",
    "RolledOut",
    "Succeeded",
    "Verified",
}


def check_condition_type(value: str) -> ConditionType:
    if value in CONDITION_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CONDITION_TYPE_VALUES!r}")
