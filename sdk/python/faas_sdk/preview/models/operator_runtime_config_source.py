from typing import Literal

OperatorRuntimeConfigSource = Literal["default_or_environment", "operator"]

OPERATOR_RUNTIME_CONFIG_SOURCE_VALUES: set[OperatorRuntimeConfigSource] = {
    "default_or_environment",
    "operator",
}


def check_operator_runtime_config_source(value: str) -> OperatorRuntimeConfigSource:
    if value in OPERATOR_RUNTIME_CONFIG_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATOR_RUNTIME_CONFIG_SOURCE_VALUES!r}")
