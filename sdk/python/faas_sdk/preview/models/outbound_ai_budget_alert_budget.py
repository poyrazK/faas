from typing import Literal

OutboundAIBudgetAlertBudget = Literal[
    "app_daily_tokens",
    "daily_cost",
    "daily_tokens",
    "monthly_cost",
    "tag_daily_cost",
    "user_daily_cost",
    "user_daily_tokens",
]

OUTBOUND_AI_BUDGET_ALERT_BUDGET_VALUES: set[OutboundAIBudgetAlertBudget] = {
    "app_daily_tokens",
    "daily_cost",
    "daily_tokens",
    "monthly_cost",
    "tag_daily_cost",
    "user_daily_cost",
    "user_daily_tokens",
}


def check_outbound_ai_budget_alert_budget(value: str) -> OutboundAIBudgetAlertBudget:
    if value in OUTBOUND_AI_BUDGET_ALERT_BUDGET_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_BUDGET_ALERT_BUDGET_VALUES!r}")
