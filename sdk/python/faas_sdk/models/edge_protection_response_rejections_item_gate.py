from typing import Literal

EdgeProtectionResponseRejectionsItemGate = Literal[
    "body_limit", "geo", "internal_only", "ip", "ip_allowlist", "jwt", "limit", "throttle"
]

EDGE_PROTECTION_RESPONSE_REJECTIONS_ITEM_GATE_VALUES: set[EdgeProtectionResponseRejectionsItemGate] = {
    "body_limit",
    "geo",
    "internal_only",
    "ip",
    "ip_allowlist",
    "jwt",
    "limit",
    "throttle",
}


def check_edge_protection_response_rejections_item_gate(value: str) -> EdgeProtectionResponseRejectionsItemGate:
    if value in EDGE_PROTECTION_RESPONSE_REJECTIONS_ITEM_GATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EDGE_PROTECTION_RESPONSE_REJECTIONS_ITEM_GATE_VALUES!r}"
    )
