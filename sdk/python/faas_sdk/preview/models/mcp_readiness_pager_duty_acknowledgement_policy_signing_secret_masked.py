from typing import Literal

MCPReadinessPagerDutyAcknowledgementPolicySigningSecretMasked = Literal["***"]

MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_POLICY_SIGNING_SECRET_MASKED_VALUES: set[
    MCPReadinessPagerDutyAcknowledgementPolicySigningSecretMasked
] = {
    "***",
}


def check_mcp_readiness_pager_duty_acknowledgement_policy_signing_secret_masked(
    value: str,
) -> MCPReadinessPagerDutyAcknowledgementPolicySigningSecretMasked:
    if value in MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_POLICY_SIGNING_SECRET_MASKED_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_POLICY_SIGNING_SECRET_MASKED_VALUES!r}"
    )
