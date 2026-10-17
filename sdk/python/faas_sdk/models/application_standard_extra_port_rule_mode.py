from typing import Literal

ApplicationStandardExtraPortRuleMode = Literal["default", "mandatory", "restricted"]

APPLICATION_STANDARD_EXTRA_PORT_RULE_MODE_VALUES: set[ApplicationStandardExtraPortRuleMode] = {
    "default",
    "mandatory",
    "restricted",
}


def check_application_standard_extra_port_rule_mode(value: str) -> ApplicationStandardExtraPortRuleMode:
    if value in APPLICATION_STANDARD_EXTRA_PORT_RULE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_EXTRA_PORT_RULE_MODE_VALUES!r}")
