from typing import Literal

PlatformTenantSurfaceCertificateChangedWebhookPayloadCertState = Literal["failed", "issued", "none", "pending"]

PLATFORM_TENANT_SURFACE_CERTIFICATE_CHANGED_WEBHOOK_PAYLOAD_CERT_STATE_VALUES: set[
    PlatformTenantSurfaceCertificateChangedWebhookPayloadCertState
] = {
    "failed",
    "issued",
    "none",
    "pending",
}


def check_platform_tenant_surface_certificate_changed_webhook_payload_cert_state(
    value: str,
) -> PlatformTenantSurfaceCertificateChangedWebhookPayloadCertState:
    if value in PLATFORM_TENANT_SURFACE_CERTIFICATE_CHANGED_WEBHOOK_PAYLOAD_CERT_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_SURFACE_CERTIFICATE_CHANGED_WEBHOOK_PAYLOAD_CERT_STATE_VALUES!r}"
    )
