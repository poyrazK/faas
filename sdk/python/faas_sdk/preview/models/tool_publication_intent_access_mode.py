from typing import Literal

ToolPublicationIntentAccessMode = Literal["external-oauth", "open"]

TOOL_PUBLICATION_INTENT_ACCESS_MODE_VALUES: set[ToolPublicationIntentAccessMode] = {
    "external-oauth",
    "open",
}


def check_tool_publication_intent_access_mode(value: str) -> ToolPublicationIntentAccessMode:
    if value in TOOL_PUBLICATION_INTENT_ACCESS_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TOOL_PUBLICATION_INTENT_ACCESS_MODE_VALUES!r}")
