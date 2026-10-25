from typing import Literal

ApplicationStandardCIDRRuleMode = Literal["default", "mandatory", "restricted"]

APPLICATION_STANDARD_CIDR_RULE_MODE_VALUES: set[ApplicationStandardCIDRRuleMode] = {
    "default",
    "mandatory",
    "restricted",
}


def check_application_standard_cidr_rule_mode(value: str) -> ApplicationStandardCIDRRuleMode:
    if value in APPLICATION_STANDARD_CIDR_RULE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_CIDR_RULE_MODE_VALUES!r}")
