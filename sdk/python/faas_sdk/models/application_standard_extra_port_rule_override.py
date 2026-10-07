from typing import Literal

ApplicationStandardExtraPortRuleOverride = Literal["narrow", "none"]

APPLICATION_STANDARD_EXTRA_PORT_RULE_OVERRIDE_VALUES: set[ApplicationStandardExtraPortRuleOverride] = {
    "narrow",
    "none",
}


def check_application_standard_extra_port_rule_override(value: str) -> ApplicationStandardExtraPortRuleOverride:
    if value in APPLICATION_STANDARD_EXTRA_PORT_RULE_OVERRIDE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_EXTRA_PORT_RULE_OVERRIDE_VALUES!r}"
    )
