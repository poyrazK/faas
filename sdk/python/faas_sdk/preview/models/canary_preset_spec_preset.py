from typing import Literal

CanaryPresetSpecPreset = Literal["1-10-50-100", "adaptive", "aggressive", "balanced", "custom", "none", "slow"]

CANARY_PRESET_SPEC_PRESET_VALUES: set[CanaryPresetSpecPreset] = {
    "1-10-50-100",
    "adaptive",
    "aggressive",
    "balanced",
    "custom",
    "none",
    "slow",
}


def check_canary_preset_spec_preset(value: str) -> CanaryPresetSpecPreset:
    if value in CANARY_PRESET_SPEC_PRESET_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CANARY_PRESET_SPEC_PRESET_VALUES!r}")
