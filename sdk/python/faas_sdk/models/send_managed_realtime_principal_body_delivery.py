from typing import Literal

SendManagedRealtimePrincipalBodyDelivery = Literal["live", "retained"]

SEND_MANAGED_REALTIME_PRINCIPAL_BODY_DELIVERY_VALUES: set[SendManagedRealtimePrincipalBodyDelivery] = {
    "live",
    "retained",
}


def check_send_managed_realtime_principal_body_delivery(value: str) -> SendManagedRealtimePrincipalBodyDelivery:
    if value in SEND_MANAGED_REALTIME_PRINCIPAL_BODY_DELIVERY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SEND_MANAGED_REALTIME_PRINCIPAL_BODY_DELIVERY_VALUES!r}"
    )
