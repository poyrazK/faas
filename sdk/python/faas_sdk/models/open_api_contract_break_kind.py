from typing import Literal

OpenAPIContractBreakKind = Literal["field_removed", "nullability_change", "required_added", "type_change"]

OPEN_API_CONTRACT_BREAK_KIND_VALUES: set[OpenAPIContractBreakKind] = {
    "field_removed",
    "nullability_change",
    "required_added",
    "type_change",
}


def check_open_api_contract_break_kind(value: str) -> OpenAPIContractBreakKind:
    if value in OPEN_API_CONTRACT_BREAK_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPEN_API_CONTRACT_BREAK_KIND_VALUES!r}")
