from typing import Literal

OutboundAIPromptEvaluationItemVerdict = Literal["candidate_error", "judge_error", "loss", "tie", "win"]

OUTBOUND_AI_PROMPT_EVALUATION_ITEM_VERDICT_VALUES: set[OutboundAIPromptEvaluationItemVerdict] = {
    "candidate_error",
    "judge_error",
    "loss",
    "tie",
    "win",
}


def check_outbound_ai_prompt_evaluation_item_verdict(value: str) -> OutboundAIPromptEvaluationItemVerdict:
    if value in OUTBOUND_AI_PROMPT_EVALUATION_ITEM_VERDICT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_PROMPT_EVALUATION_ITEM_VERDICT_VALUES!r}"
    )
