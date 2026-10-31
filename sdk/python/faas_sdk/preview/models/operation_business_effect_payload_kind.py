from typing import Literal

OperationBusinessEffectPayloadKind = Literal["gregale.business-effect.v1"]

OPERATION_BUSINESS_EFFECT_PAYLOAD_KIND_VALUES: set[OperationBusinessEffectPayloadKind] = {
    "gregale.business-effect.v1",
}


def check_operation_business_effect_payload_kind(value: str) -> OperationBusinessEffectPayloadKind:
    if value in OPERATION_BUSINESS_EFFECT_PAYLOAD_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_BUSINESS_EFFECT_PAYLOAD_KIND_VALUES!r}")
