from typing import Literal

MCPReadinessPagerDutyAcknowledgementEventAction = Literal["acknowledge", "release"]

MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_EVENT_ACTION_VALUES: set[MCPReadinessPagerDutyAcknowledgementEventAction] = {
    "acknowledge",
    "release",
}


def check_mcp_readiness_pager_duty_acknowledgement_event_action(
    value: str,
) -> MCPReadinessPagerDutyAcknowledgementEventAction:
    if value in MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_EVENT_ACTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_READINESS_PAGER_DUTY_ACKNOWLEDGEMENT_EVENT_ACTION_VALUES!r}"
    )
