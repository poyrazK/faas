from typing import Literal

ListAccountWorkflowAttentionSort = Literal["deadline", "updated_at"]

LIST_ACCOUNT_WORKFLOW_ATTENTION_SORT_VALUES: set[ListAccountWorkflowAttentionSort] = {
    "deadline",
    "updated_at",
}


def check_list_account_workflow_attention_sort(value: str) -> ListAccountWorkflowAttentionSort:
    if value in LIST_ACCOUNT_WORKFLOW_ATTENTION_SORT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_ACCOUNT_WORKFLOW_ATTENTION_SORT_VALUES!r}")
