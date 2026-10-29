from typing import Literal

ApplicationStandardSecurityPolicyRuleValue = Literal["enforce", "off", "warn"]

APPLICATION_STANDARD_SECURITY_POLICY_RULE_VALUE_VALUES: set[ApplicationStandardSecurityPolicyRuleValue] = {
    "enforce",
    "off",
    "warn",
}


def check_application_standard_security_policy_rule_value(value: str) -> ApplicationStandardSecurityPolicyRuleValue:
    if value in APPLICATION_STANDARD_SECURITY_POLICY_RULE_VALUE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_SECURITY_POLICY_RULE_VALUE_VALUES!r}"
    )
