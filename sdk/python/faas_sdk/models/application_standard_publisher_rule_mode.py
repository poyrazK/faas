from typing import Literal

ApplicationStandardPublisherRuleMode = Literal["default", "mandatory", "restricted"]

APPLICATION_STANDARD_PUBLISHER_RULE_MODE_VALUES: set[ApplicationStandardPublisherRuleMode] = {
    "default",
    "mandatory",
    "restricted",
}


def check_application_standard_publisher_rule_mode(value: str) -> ApplicationStandardPublisherRuleMode:
    if value in APPLICATION_STANDARD_PUBLISHER_RULE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_PUBLISHER_RULE_MODE_VALUES!r}")
