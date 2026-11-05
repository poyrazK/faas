from typing import Literal

WorkloadPortProtocol = Literal["tcp", "udp"]

WORKLOAD_PORT_PROTOCOL_VALUES: set[WorkloadPortProtocol] = {
    "tcp",
    "udp",
}


def check_workload_port_protocol(value: str) -> WorkloadPortProtocol:
    if value in WORKLOAD_PORT_PROTOCOL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKLOAD_PORT_PROTOCOL_VALUES!r}")
