from typing import Literal

ApplicationStandardCIDRRuleOverride = Literal["narrow", "none"]

APPLICATION_STANDARD_CIDR_RULE_OVERRIDE_VALUES: set[ApplicationStandardCIDRRuleOverride] = {
    "narrow",
    "none",
}


def check_application_standard_cidr_rule_override(value: str) -> ApplicationStandardCIDRRuleOverride:
    if value in APPLICATION_STANDARD_CIDR_RULE_OVERRIDE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_CIDR_RULE_OVERRIDE_VALUES!r}")
