from typing import Literal

DataEndpointCapacityLimitingResource = Literal["connections", "logical_workers", "replication_slots", "wal_senders"]

DATA_ENDPOINT_CAPACITY_LIMITING_RESOURCE_VALUES: set[DataEndpointCapacityLimitingResource] = {
    "connections",
    "logical_workers",
    "replication_slots",
    "wal_senders",
}


def check_data_endpoint_capacity_limiting_resource(value: str) -> DataEndpointCapacityLimitingResource:
    if value in DATA_ENDPOINT_CAPACITY_LIMITING_RESOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_ENDPOINT_CAPACITY_LIMITING_RESOURCE_VALUES!r}")
