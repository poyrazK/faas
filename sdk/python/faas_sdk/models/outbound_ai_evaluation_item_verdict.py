from typing import Literal

OutboundAIEvaluationItemVerdict = Literal["candidate_error", "judge_error", "loss", "tie", "win"]

OUTBOUND_AI_EVALUATION_ITEM_VERDICT_VALUES: set[OutboundAIEvaluationItemVerdict] = {
    "candidate_error",
    "judge_error",
    "loss",
    "tie",
    "win",
}


def check_outbound_ai_evaluation_item_verdict(value: str) -> OutboundAIEvaluationItemVerdict:
    if value in OUTBOUND_AI_EVALUATION_ITEM_VERDICT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_EVALUATION_ITEM_VERDICT_VALUES!r}")
