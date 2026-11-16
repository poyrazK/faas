from typing import Literal

AgentUndoItemKind = Literal["create", "delete", "deploy", "other", "settings"]

AGENT_UNDO_ITEM_KIND_VALUES: set[AgentUndoItemKind] = {
    "create",
    "delete",
    "deploy",
    "other",
    "settings",
}


def check_agent_undo_item_kind(value: str) -> AgentUndoItemKind:
    if value in AGENT_UNDO_ITEM_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AGENT_UNDO_ITEM_KIND_VALUES!r}")
