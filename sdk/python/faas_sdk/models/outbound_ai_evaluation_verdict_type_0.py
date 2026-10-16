from typing import Literal

OutboundAIEvaluationVerdictType0 = Literal["fail", "pass"]

OUTBOUND_AI_EVALUATION_VERDICT_TYPE_0_VALUES: set[OutboundAIEvaluationVerdictType0] = {
    "fail",
    "pass",
}


def check_outbound_ai_evaluation_verdict_type_0(value: str) -> OutboundAIEvaluationVerdictType0:
    if value in OUTBOUND_AI_EVALUATION_VERDICT_TYPE_0_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_EVALUATION_VERDICT_TYPE_0_VALUES!r}")
