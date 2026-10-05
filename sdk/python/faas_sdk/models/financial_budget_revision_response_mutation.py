from typing import Literal

FinancialBudgetRevisionResponseMutation = Literal["created", "deleted", "updated"]

FINANCIAL_BUDGET_REVISION_RESPONSE_MUTATION_VALUES: set[FinancialBudgetRevisionResponseMutation] = {
    "created",
    "deleted",
    "updated",
}


def check_financial_budget_revision_response_mutation(value: str) -> FinancialBudgetRevisionResponseMutation:
    if value in FINANCIAL_BUDGET_REVISION_RESPONSE_MUTATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {FINANCIAL_BUDGET_REVISION_RESPONSE_MUTATION_VALUES!r}"
    )
