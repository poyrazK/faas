from typing import Literal

OutboundBindingProbePolicyMethod = Literal["GET", "HEAD"]

OUTBOUND_BINDING_PROBE_POLICY_METHOD_VALUES: set[OutboundBindingProbePolicyMethod] = {
    "GET",
    "HEAD",
}


def check_outbound_binding_probe_policy_method(value: str) -> OutboundBindingProbePolicyMethod:
    if value in OUTBOUND_BINDING_PROBE_POLICY_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_BINDING_PROBE_POLICY_METHOD_VALUES!r}")
