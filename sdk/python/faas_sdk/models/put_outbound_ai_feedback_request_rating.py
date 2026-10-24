from typing import Literal

PutOutboundAIFeedbackRequestRating = Literal["bad", "good"]

PUT_OUTBOUND_AI_FEEDBACK_REQUEST_RATING_VALUES: set[PutOutboundAIFeedbackRequestRating] = {
    "bad",
    "good",
}


def check_put_outbound_ai_feedback_request_rating(value: str) -> PutOutboundAIFeedbackRequestRating:
    if value in PUT_OUTBOUND_AI_FEEDBACK_REQUEST_RATING_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUT_OUTBOUND_AI_FEEDBACK_REQUEST_RATING_VALUES!r}")
