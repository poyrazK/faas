from typing import Literal

ManagedRealtimePrincipalMessageRequestDelivery = Literal["live", "retained"]

MANAGED_REALTIME_PRINCIPAL_MESSAGE_REQUEST_DELIVERY_VALUES: set[ManagedRealtimePrincipalMessageRequestDelivery] = {
    "live",
    "retained",
}


def check_managed_realtime_principal_message_request_delivery(
    value: str,
) -> ManagedRealtimePrincipalMessageRequestDelivery:
    if value in MANAGED_REALTIME_PRINCIPAL_MESSAGE_REQUEST_DELIVERY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_REALTIME_PRINCIPAL_MESSAGE_REQUEST_DELIVERY_VALUES!r}"
    )
