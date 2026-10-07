from typing import Literal

GetCellAppBindingTrafficOrigin = Literal[1]

GET_CELL_APP_BINDING_TRAFFIC_ORIGIN_VALUES: set[GetCellAppBindingTrafficOrigin] = {
    1,
}


def check_get_cell_app_binding_traffic_origin(value: int) -> GetCellAppBindingTrafficOrigin:
    if value in GET_CELL_APP_BINDING_TRAFFIC_ORIGIN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_CELL_APP_BINDING_TRAFFIC_ORIGIN_VALUES!r}")
