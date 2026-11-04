from typing import Literal

ConfigRevisionSource = Literal[
    "baseline",
    "config.guard_restore",
    "config.rollback",
    "env.delete",
    "env.set",
    "secret.delete",
    "secret.rotate",
    "secret.set",
]

CONFIG_REVISION_SOURCE_VALUES: set[ConfigRevisionSource] = {
    "baseline",
    "config.guard_restore",
    "config.rollback",
    "env.delete",
    "env.set",
    "secret.delete",
    "secret.rotate",
    "secret.set",
}


def check_config_revision_source(value: str) -> ConfigRevisionSource:
    if value in CONFIG_REVISION_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CONFIG_REVISION_SOURCE_VALUES!r}")
