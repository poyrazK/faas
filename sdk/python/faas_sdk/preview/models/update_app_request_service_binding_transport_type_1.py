from typing import Literal

UpdateAppRequestServiceBindingTransportType1 = Literal["http", "https"]

UPDATE_APP_REQUEST_SERVICE_BINDING_TRANSPORT_TYPE_1_VALUES: set[UpdateAppRequestServiceBindingTransportType1] = {
    "http",
    "https",
}


def check_update_app_request_service_binding_transport_type_1(
    value: str,
) -> UpdateAppRequestServiceBindingTransportType1:
    if value in UPDATE_APP_REQUEST_SERVICE_BINDING_TRANSPORT_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_APP_REQUEST_SERVICE_BINDING_TRANSPORT_TYPE_1_VALUES!r}"
    )
