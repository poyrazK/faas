from typing import Literal

MCPReadinessPagerDutyAcknowledgementEventOutcome = Literal[
    "acknowledged", "already_acknowledged", "not_acknowledged", "released", "resolved", "retained", "stale"
]

MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_EVENT_OUTCOME_VALUES: set[MCPReadinessPagerDutyAcknowledgementEventOutcome] = {
    "acknowledged",
    "already_acknowledged",
    "not_acknowledged",
    "released",
    "resolved",
    "retained",
    "stale",
}


def check_mcp_readiness_pager_duty_acknowledgement_event_outcome(
    value: str,
) -> MCPReadinessPagerDutyAcknowledgementEventOutcome:
    if value in MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_EVENT_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_EVENT_OUTCOME_VALUES!r}"
    )
