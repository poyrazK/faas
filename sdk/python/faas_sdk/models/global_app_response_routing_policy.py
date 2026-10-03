from typing import Literal

GlobalAppResponseRoutingPolicy = Literal["all_selected_regions"]

GLOBAL_APP_RESPONSE_ROUTING_POLICY_VALUES: set[GlobalAppResponseRoutingPolicy] = {
    "all_selected_regions",
}


def check_global_app_response_routing_policy(value: str) -> GlobalAppResponseRoutingPolicy:
    if value in GLOBAL_APP_RESPONSE_ROUTING_POLICY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GLOBAL_APP_RESPONSE_ROUTING_POLICY_VALUES!r}")
