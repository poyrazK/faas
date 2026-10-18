from typing import Literal

SLOResponseSli = Literal["availability", "latency"]

SLO_RESPONSE_SLI_VALUES: set[SLOResponseSli] = {
    "availability",
    "latency",
}


def check_slo_response_sli(value: str) -> SLOResponseSli:
    if value in SLO_RESPONSE_SLI_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SLO_RESPONSE_SLI_VALUES!r}")
