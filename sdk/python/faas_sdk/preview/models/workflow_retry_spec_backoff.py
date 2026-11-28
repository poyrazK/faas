from typing import Literal

WorkflowRetrySpecBackoff = Literal["exponential", "fixed"]

WORKFLOW_RETRY_SPEC_BACKOFF_VALUES: set[WorkflowRetrySpecBackoff] = {
    "exponential",
    "fixed",
}


def check_workflow_retry_spec_backoff(value: str) -> WorkflowRetrySpecBackoff:
    if value in WORKFLOW_RETRY_SPEC_BACKOFF_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_RETRY_SPEC_BACKOFF_VALUES!r}")
