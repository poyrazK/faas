from typing import Literal

ObjectStoragePolicyAccountingMode = Literal["gateway_safety_v1"]

OBJECT_STORAGE_POLICY_ACCOUNTING_MODE_VALUES: set[ObjectStoragePolicyAccountingMode] = {
    "gateway_safety_v1",
}


def check_object_storage_policy_accounting_mode(value: str) -> ObjectStoragePolicyAccountingMode:
    if value in OBJECT_STORAGE_POLICY_ACCOUNTING_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_STORAGE_POLICY_ACCOUNTING_MODE_VALUES!r}")
