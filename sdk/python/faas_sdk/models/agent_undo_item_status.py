from typing import Literal

AgentUndoItemStatus = Literal["changed_since", "not_undoable", "undoable", "undone"]

AGENT_UNDO_ITEM_STATUS_VALUES: set[AgentUndoItemStatus] = {
    "changed_since",
    "not_undoable",
    "undoable",
    "undone",
}


def check_agent_undo_item_status(value: str) -> AgentUndoItemStatus:
    if value in AGENT_UNDO_ITEM_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AGENT_UNDO_ITEM_STATUS_VALUES!r}")
