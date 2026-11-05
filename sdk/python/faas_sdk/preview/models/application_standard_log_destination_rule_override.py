from typing import Literal

ApplicationStandardLogDestinationRuleOverride = Literal["extend", "none"]

APPLICATION_STANDARD_LOG_DESTINATION_RULE_OVERRIDE_VALUES: set[ApplicationStandardLogDestinationRuleOverride] = {
    "extend",
    "none",
}


def check_application_standard_log_destination_rule_override(
    value: str,
) -> ApplicationStandardLogDestinationRuleOverride:
    if value in APPLICATION_STANDARD_LOG_DESTINATION_RULE_OVERRIDE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_LOG_DESTINATION_RULE_OVERRIDE_VALUES!r}"
    )
