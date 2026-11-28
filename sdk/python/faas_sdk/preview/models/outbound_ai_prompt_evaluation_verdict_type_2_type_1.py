from typing import Literal

OutboundAIPromptEvaluationVerdictType2Type1 = Literal["fail", "pass"]

OUTBOUND_AI_PROMPT_EVALUATION_VERDICT_TYPE_2_TYPE_1_VALUES: set[OutboundAIPromptEvaluationVerdictType2Type1] = {
    "fail",
    "pass",
}


def check_outbound_ai_prompt_evaluation_verdict_type_2_type_1(
    value: str,
) -> OutboundAIPromptEvaluationVerdictType2Type1:
    if value in OUTBOUND_AI_PROMPT_EVALUATION_VERDICT_TYPE_2_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_PROMPT_EVALUATION_VERDICT_TYPE_2_TYPE_1_VALUES!r}"
    )
