from typing import Literal

AgentUndoItemResult = Literal["changed_since", "failed", "skipped", "undone"]

AGENT_UNDO_ITEM_RESULT_VALUES: set[AgentUndoItemResult] = {
    "changed_since",
    "failed",
    "skipped",
    "undone",
}


def check_agent_undo_item_result(value: str) -> AgentUndoItemResult:
    if value in AGENT_UNDO_ITEM_RESULT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AGENT_UNDO_ITEM_RESULT_VALUES!r}")
