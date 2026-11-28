from typing import Literal

ListEgressFlowsVerdict = Literal["allowed", "blocked"]

LIST_EGRESS_FLOWS_VERDICT_VALUES: set[ListEgressFlowsVerdict] = {
    "allowed",
    "blocked",
}


def check_list_egress_flows_verdict(value: str) -> ListEgressFlowsVerdict:
    if value in LIST_EGRESS_FLOWS_VERDICT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_EGRESS_FLOWS_VERDICT_VALUES!r}")
