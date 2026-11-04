from typing import Literal

SummarizePlatformTenantSelfWorkflowOutcomesGroupBy = Literal["outcome", "workflow"]

SUMMARIZE_PLATFORM_TENANT_SELF_WORKFLOW_OUTCOMES_GROUP_BY_VALUES: set[
    SummarizePlatformTenantSelfWorkflowOutcomesGroupBy
] = {
    "outcome",
    "workflow",
}


def check_summarize_platform_tenant_self_workflow_outcomes_group_by(
    value: str,
) -> SummarizePlatformTenantSelfWorkflowOutcomesGroupBy:
    if value in SUMMARIZE_PLATFORM_TENANT_SELF_WORKFLOW_OUTCOMES_GROUP_BY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SUMMARIZE_PLATFORM_TENANT_SELF_WORKFLOW_OUTCOMES_GROUP_BY_VALUES!r}"
    )
