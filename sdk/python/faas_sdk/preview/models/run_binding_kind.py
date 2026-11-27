from typing import Literal

RunBindingKind = Literal["agent_endpoint", "api_key"]

RUN_BINDING_KIND_VALUES: set[RunBindingKind] = {
    "agent_endpoint",
    "api_key",
}


def check_run_binding_kind(value: str) -> RunBindingKind:
    if value in RUN_BINDING_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RUN_BINDING_KIND_VALUES!r}")
