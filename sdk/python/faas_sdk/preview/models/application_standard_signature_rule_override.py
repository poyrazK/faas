from typing import Literal

ApplicationStandardSignatureRuleOverride = Literal["narrow", "none"]

APPLICATION_STANDARD_SIGNATURE_RULE_OVERRIDE_VALUES: set[ApplicationStandardSignatureRuleOverride] = {
    "narrow",
    "none",
}


def check_application_standard_signature_rule_override(value: str) -> ApplicationStandardSignatureRuleOverride:
    if value in APPLICATION_STANDARD_SIGNATURE_RULE_OVERRIDE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_SIGNATURE_RULE_OVERRIDE_VALUES!r}"
    )
