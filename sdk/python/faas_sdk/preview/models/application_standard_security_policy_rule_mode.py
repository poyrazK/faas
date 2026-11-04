from typing import Literal

ApplicationStandardSecurityPolicyRuleMode = Literal["default", "mandatory", "restricted"]

APPLICATION_STANDARD_SECURITY_POLICY_RULE_MODE_VALUES: set[ApplicationStandardSecurityPolicyRuleMode] = {
    "default",
    "mandatory",
    "restricted",
}


def check_application_standard_security_policy_rule_mode(value: str) -> ApplicationStandardSecurityPolicyRuleMode:
    if value in APPLICATION_STANDARD_SECURITY_POLICY_RULE_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_SECURITY_POLICY_RULE_MODE_VALUES!r}"
    )
