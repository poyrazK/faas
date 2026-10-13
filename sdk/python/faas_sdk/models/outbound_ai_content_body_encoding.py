from typing import Literal

OutboundAIContentBodyEncoding = Literal["base64", "utf-8"]

OUTBOUND_AI_CONTENT_BODY_ENCODING_VALUES: set[OutboundAIContentBodyEncoding] = {
    "base64",
    "utf-8",
}


def check_outbound_ai_content_body_encoding(value: str) -> OutboundAIContentBodyEncoding:
    if value in OUTBOUND_AI_CONTENT_BODY_ENCODING_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_CONTENT_BODY_ENCODING_VALUES!r}")
