from typing import Literal

AgentBudgetResponseSource = Literal["key", "org_ceiling", "plan_default", "unlimited"]

AGENT_BUDGET_RESPONSE_SOURCE_VALUES: set[AgentBudgetResponseSource] = {
    "key",
    "org_ceiling",
    "plan_default",
    "unlimited",
}


def check_agent_budget_response_source(value: str) -> AgentBudgetResponseSource:
    if value in AGENT_BUDGET_RESPONSE_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AGENT_BUDGET_RESPONSE_SOURCE_VALUES!r}")
