from typing import Literal

DomainBindingChangeAction = Literal["create", "move", "noop"]

DOMAIN_BINDING_CHANGE_ACTION_VALUES: set[DomainBindingChangeAction] = {
    "create",
    "move",
    "noop",
}


def check_domain_binding_change_action(value: str) -> DomainBindingChangeAction:
    if value in DOMAIN_BINDING_CHANGE_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DOMAIN_BINDING_CHANGE_ACTION_VALUES!r}")
