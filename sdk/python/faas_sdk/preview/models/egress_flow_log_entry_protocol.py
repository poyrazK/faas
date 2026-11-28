from typing import Literal

EgressFlowLogEntryProtocol = Literal["tcp", "udp"]

EGRESS_FLOW_LOG_ENTRY_PROTOCOL_VALUES: set[EgressFlowLogEntryProtocol] = {
    "tcp",
    "udp",
}


def check_egress_flow_log_entry_protocol(value: str) -> EgressFlowLogEntryProtocol:
    if value in EGRESS_FLOW_LOG_ENTRY_PROTOCOL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EGRESS_FLOW_LOG_ENTRY_PROTOCOL_VALUES!r}")
