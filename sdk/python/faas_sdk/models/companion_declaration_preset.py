from typing import Literal

CompanionDeclarationPreset = Literal["datadog-dogstatsd", "opentelemetry", "sentry"]

COMPANION_DECLARATION_PRESET_VALUES: set[CompanionDeclarationPreset] = {
    "datadog-dogstatsd",
    "opentelemetry",
    "sentry",
}


def check_companion_declaration_preset(value: str) -> CompanionDeclarationPreset:
    if value in COMPANION_DECLARATION_PRESET_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {COMPANION_DECLARATION_PRESET_VALUES!r}")
