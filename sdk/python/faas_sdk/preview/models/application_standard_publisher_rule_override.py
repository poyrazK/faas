from typing import Literal

ApplicationStandardPublisherRuleOverride = Literal["narrow", "none"]

APPLICATION_STANDARD_PUBLISHER_RULE_OVERRIDE_VALUES: set[ApplicationStandardPublisherRuleOverride] = {
    "narrow",
    "none",
}


def check_application_standard_publisher_rule_override(value: str) -> ApplicationStandardPublisherRuleOverride:
    if value in APPLICATION_STANDARD_PUBLISHER_RULE_OVERRIDE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_PUBLISHER_RULE_OVERRIDE_VALUES!r}"
    )
