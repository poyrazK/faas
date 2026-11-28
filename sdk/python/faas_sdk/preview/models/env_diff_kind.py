from typing import Literal

EnvDiffKind = Literal["env", "secret"]

ENV_DIFF_KIND_VALUES: set[EnvDiffKind] = {
    "env",
    "secret",
}


def check_env_diff_kind(value: str) -> EnvDiffKind:
    if value in ENV_DIFF_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENV_DIFF_KIND_VALUES!r}")
