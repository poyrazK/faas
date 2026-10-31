from typing import Literal

FilterCriteriaOp = Literal["eq", "exists", "jsonpath", "neq"]

FILTER_CRITERIA_OP_VALUES: set[FilterCriteriaOp] = {
    "eq",
    "exists",
    "jsonpath",
    "neq",
}


def check_filter_criteria_op(value: str) -> FilterCriteriaOp:
    if value in FILTER_CRITERIA_OP_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FILTER_CRITERIA_OP_VALUES!r}")
