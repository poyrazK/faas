from typing import Literal

ApplicationStandardSignatureRuleMode = Literal["default", "mandatory", "restricted"]

APPLICATION_STANDARD_SIGNATURE_RULE_MODE_VALUES: set[ApplicationStandardSignatureRuleMode] = {
    "default",
    "mandatory",
    "restricted",
}


def check_application_standard_signature_rule_mode(value: str) -> ApplicationStandardSignatureRuleMode:
    if value in APPLICATION_STANDARD_SIGNATURE_RULE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_SIGNATURE_RULE_MODE_VALUES!r}")
