from typing import Literal

ApplicationStandardSecurityPolicyRuleOverride = Literal["narrow", "none"]

APPLICATION_STANDARD_SECURITY_POLICY_RULE_OVERRIDE_VALUES: set[ApplicationStandardSecurityPolicyRuleOverride] = {
    "narrow",
    "none",
}


def check_application_standard_security_policy_rule_override(
    value: str,
) -> ApplicationStandardSecurityPolicyRuleOverride:
    if value in APPLICATION_STANDARD_SECURITY_POLICY_RULE_OVERRIDE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_SECURITY_POLICY_RULE_OVERRIDE_VALUES!r}"
    )
