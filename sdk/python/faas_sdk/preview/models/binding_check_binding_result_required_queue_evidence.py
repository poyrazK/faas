from typing import Literal

BindingCheckBindingResultRequiredQueueEvidence = Literal["consumer_readiness", "handler_completion"]

BINDING_CHECK_BINDING_RESULT_REQUIRED_QUEUE_EVIDENCE_VALUES: set[BindingCheckBindingResultRequiredQueueEvidence] = {
    "consumer_readiness",
    "handler_completion",
}


def check_binding_check_binding_result_required_queue_evidence(
    value: str,
) -> BindingCheckBindingResultRequiredQueueEvidence:
    if value in BINDING_CHECK_BINDING_RESULT_REQUIRED_QUEUE_EVIDENCE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {BINDING_CHECK_BINDING_RESULT_REQUIRED_QUEUE_EVIDENCE_VALUES!r}"
    )
