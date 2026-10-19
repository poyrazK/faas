from typing import Literal

OperatorRuntimeConfigKind = Literal[
    "boolean", "duration", "enum", "integer", "platform_flag", "platform_flag_groups", "secret_reference", "string"
]

OPERATOR_RUNTIME_CONFIG_KIND_VALUES: set[OperatorRuntimeConfigKind] = {
    "boolean",
    "duration",
    "enum",
    "integer",
    "platform_flag",
    "platform_flag_groups",
    "secret_reference",
    "string",
}


def check_operator_runtime_config_kind(value: str) -> OperatorRuntimeConfigKind:
    if value in OPERATOR_RUNTIME_CONFIG_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATOR_RUNTIME_CONFIG_KIND_VALUES!r}")
