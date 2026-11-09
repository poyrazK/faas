from typing import Literal

AgentActivityEntryMethod = Literal["DELETE", "PATCH", "POST", "PUT"]

AGENT_ACTIVITY_ENTRY_METHOD_VALUES: set[AgentActivityEntryMethod] = {
    "DELETE",
    "PATCH",
    "POST",
    "PUT",
}


def check_agent_activity_entry_method(value: str) -> AgentActivityEntryMethod:
    if value in AGENT_ACTIVITY_ENTRY_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AGENT_ACTIVITY_ENTRY_METHOD_VALUES!r}")
