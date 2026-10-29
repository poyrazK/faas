from typing import Literal

CreateSLORequestSli = Literal["availability", "latency"]

CREATE_SLO_REQUEST_SLI_VALUES: set[CreateSLORequestSli] = {
    "availability",
    "latency",
}


def check_create_slo_request_sli(value: str) -> CreateSLORequestSli:
    if value in CREATE_SLO_REQUEST_SLI_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_SLO_REQUEST_SLI_VALUES!r}")
