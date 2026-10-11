from typing import Literal

ManagedOperationResultGregaleOperationResult = Literal[1]

MANAGED_OPERATION_RESULT_GREGALE_OPERATION_RESULT_VALUES: set[ManagedOperationResultGregaleOperationResult] = {
    1,
}


def check_managed_operation_result_gregale_operation_result(value: int) -> ManagedOperationResultGregaleOperationResult:
    if value in MANAGED_OPERATION_RESULT_GREGALE_OPERATION_RESULT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_OPERATION_RESULT_GREGALE_OPERATION_RESULT_VALUES!r}"
    )
