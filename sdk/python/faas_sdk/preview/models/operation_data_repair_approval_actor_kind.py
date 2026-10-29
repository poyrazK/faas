from typing import Literal

OperationDataRepairApprovalActorKind = Literal["account"]

OPERATION_DATA_REPAIR_APPROVAL_ACTOR_KIND_VALUES: set[OperationDataRepairApprovalActorKind] = {
    "account",
}


def check_operation_data_repair_approval_actor_kind(value: str) -> OperationDataRepairApprovalActorKind:
    if value in OPERATION_DATA_REPAIR_APPROVAL_ACTOR_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_DATA_REPAIR_APPROVAL_ACTOR_KIND_VALUES!r}")
