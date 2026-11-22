from typing import Literal

RunBudgetKind = Literal["customer", "task"]

RUN_BUDGET_KIND_VALUES: set[RunBudgetKind] = {
    "customer",
    "task",
}


def check_run_budget_kind(value: str) -> RunBudgetKind:
    if value in RUN_BUDGET_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RUN_BUDGET_KIND_VALUES!r}")
