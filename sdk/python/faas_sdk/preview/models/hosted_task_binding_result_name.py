from typing import Literal

HostedTaskBindingResultName = Literal[
    "configured_payload_keys",
    "database_schema",
    "migration_owner",
    "namespace",
    "observer_read_only",
    "owner_keys",
    "runtime_roles",
]

HOSTED_TASK_BINDING_RESULT_NAME_VALUES: set[HostedTaskBindingResultName] = {
    "configured_payload_keys",
    "database_schema",
    "migration_owner",
    "namespace",
    "observer_read_only",
    "owner_keys",
    "runtime_roles",
}


def check_hosted_task_binding_result_name(value: str) -> HostedTaskBindingResultName:
    if value in HOSTED_TASK_BINDING_RESULT_NAME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {HOSTED_TASK_BINDING_RESULT_NAME_VALUES!r}")
