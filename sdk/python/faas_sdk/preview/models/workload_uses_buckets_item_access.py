from typing import Literal

WorkloadUsesBucketsItemAccess = Literal["read", "read_write", "write"]

WORKLOAD_USES_BUCKETS_ITEM_ACCESS_VALUES: set[WorkloadUsesBucketsItemAccess] = {
    "read",
    "read_write",
    "write",
}


def check_workload_uses_buckets_item_access(value: str) -> WorkloadUsesBucketsItemAccess:
    if value in WORKLOAD_USES_BUCKETS_ITEM_ACCESS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKLOAD_USES_BUCKETS_ITEM_ACCESS_VALUES!r}")
