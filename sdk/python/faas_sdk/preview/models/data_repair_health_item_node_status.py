from typing import Literal

DataRepairHealthItemNodeStatus = Literal[
    "available", "heartbeat_stale", "missing", "retired", "unassigned", "unavailable"
]

DATA_REPAIR_HEALTH_ITEM_NODE_STATUS_VALUES: set[DataRepairHealthItemNodeStatus] = {
    "available",
    "heartbeat_stale",
    "missing",
    "retired",
    "unassigned",
    "unavailable",
}


def check_data_repair_health_item_node_status(value: str) -> DataRepairHealthItemNodeStatus:
    if value in DATA_REPAIR_HEALTH_ITEM_NODE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_REPAIR_HEALTH_ITEM_NODE_STATUS_VALUES!r}")
