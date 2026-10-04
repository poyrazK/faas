from typing import Literal

ObjectLifecycleRuleStatus = Literal["Disabled", "Enabled"]

OBJECT_LIFECYCLE_RULE_STATUS_VALUES: set[ObjectLifecycleRuleStatus] = {
    "Disabled",
    "Enabled",
}


def check_object_lifecycle_rule_status(value: str) -> ObjectLifecycleRuleStatus:
    if value in OBJECT_LIFECYCLE_RULE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_LIFECYCLE_RULE_STATUS_VALUES!r}")
