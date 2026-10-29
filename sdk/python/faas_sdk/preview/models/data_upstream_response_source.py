from typing import Literal

DataUpstreamResponseSource = Literal["explicit", "inferred"]

DATA_UPSTREAM_RESPONSE_SOURCE_VALUES: set[DataUpstreamResponseSource] = {
    "explicit",
    "inferred",
}


def check_data_upstream_response_source(value: str) -> DataUpstreamResponseSource:
    if value in DATA_UPSTREAM_RESPONSE_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_UPSTREAM_RESPONSE_SOURCE_VALUES!r}")
