from typing import Literal

ApplicationStandardLogDestinationRuleMode = Literal["default", "mandatory"]

APPLICATION_STANDARD_LOG_DESTINATION_RULE_MODE_VALUES: set[ApplicationStandardLogDestinationRuleMode] = {
    "default",
    "mandatory",
}


def check_application_standard_log_destination_rule_mode(value: str) -> ApplicationStandardLogDestinationRuleMode:
    if value in APPLICATION_STANDARD_LOG_DESTINATION_RULE_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_LOG_DESTINATION_RULE_MODE_VALUES!r}"
    )
