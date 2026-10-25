from typing import Literal

FeatureFlagType = Literal["boolean", "variant"]

FEATURE_FLAG_TYPE_VALUES: set[FeatureFlagType] = {
    "boolean",
    "variant",
}


def check_feature_flag_type(value: str) -> FeatureFlagType:
    if value in FEATURE_FLAG_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FEATURE_FLAG_TYPE_VALUES!r}")
