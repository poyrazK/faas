from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scoped_app_secret_response_delivery_status import (
    ScopedAppSecretResponseDeliveryStatus,
    check_scoped_app_secret_response_delivery_status,
)
from ..models.scoped_app_secret_response_last_delivery_error_code import (
    ScopedAppSecretResponseLastDeliveryErrorCode,
    check_scoped_app_secret_response_last_delivery_error_code,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ScopedAppSecretResponse")


@_attrs_define
class ScopedAppSecretResponse:
    """Per-row shape for the nested `secrets_by_scope` response
    (ADR-092 PR-B, mirror of ADR-090 D3's env_by_scope).
    Same posture as AppSecretResponse but with an explicit
    `scope` field that carries the scope name on the wire
    so a CLI / dashboard can render "scope: staging" without
    a second lookup. Value is NEVER echoed (same posture as
    AppSecretResponse).

    """

    scope: str
    key: str
    created_at: datetime.datetime
    updated_at: datetime.datetime
    delivery_version: int
    """Opaque monotonic version of the runtime value."""
    delivery_status: ScopedAppSecretResponseDeliveryStatus
    kid: str | Unset = UNSET
    """age-1... recipient string of the host identity that sealed this row (ADR-089). Empty for rows sealed before
    migration 00166. Mirrors the `kid` field on the parent `AppSecretResponse` — see that schema for the cross-
    reference."""
    value_hash: str | Unset = UNSET
    """16-hex HMAC-SHA256(plaintext) keyed by the per-host host.hmac.key (ADR-117 PR-C). Empty for pre-PR-C rows."""
    delivered_version: int | Unset = UNSET
    """Newest version confirmed in a successfully started runtime."""
    last_delivery_attempt_at: datetime.datetime | Unset = UNSET
    last_delivered_at: datetime.datetime | Unset = UNSET
    last_delivery_error_code: ScopedAppSecretResponseLastDeliveryErrorCode | Unset = UNSET
    last_delivered_wake_id: str | Unset = UNSET
    last_delivered_instance_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scope = self.scope

        key = self.key

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        delivery_version = self.delivery_version

        delivery_status: str = self.delivery_status

        kid = self.kid

        value_hash = self.value_hash

        delivered_version = self.delivered_version

        last_delivery_attempt_at: str | Unset = UNSET
        if not isinstance(self.last_delivery_attempt_at, Unset):
            last_delivery_attempt_at = self.last_delivery_attempt_at.isoformat()

        last_delivered_at: str | Unset = UNSET
        if not isinstance(self.last_delivered_at, Unset):
            last_delivered_at = self.last_delivered_at.isoformat()

        last_delivery_error_code: str | Unset = UNSET
        if not isinstance(self.last_delivery_error_code, Unset):
            last_delivery_error_code = self.last_delivery_error_code

        last_delivered_wake_id = self.last_delivered_wake_id

        last_delivered_instance_id = self.last_delivered_instance_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scope": scope,
                "key": key,
                "created_at": created_at,
                "updated_at": updated_at,
                "delivery_version": delivery_version,
                "delivery_status": delivery_status,
            }
        )
        if kid is not UNSET:
            field_dict["kid"] = kid
        if value_hash is not UNSET:
            field_dict["value_hash"] = value_hash
        if delivered_version is not UNSET:
            field_dict["delivered_version"] = delivered_version
        if last_delivery_attempt_at is not UNSET:
            field_dict["last_delivery_attempt_at"] = last_delivery_attempt_at
        if last_delivered_at is not UNSET:
            field_dict["last_delivered_at"] = last_delivered_at
        if last_delivery_error_code is not UNSET:
            field_dict["last_delivery_error_code"] = last_delivery_error_code
        if last_delivered_wake_id is not UNSET:
            field_dict["last_delivered_wake_id"] = last_delivered_wake_id
        if last_delivered_instance_id is not UNSET:
            field_dict["last_delivered_instance_id"] = last_delivered_instance_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        scope = d.pop("scope")

        key = d.pop("key")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        delivery_version = d.pop("delivery_version")

        delivery_status = check_scoped_app_secret_response_delivery_status(d.pop("delivery_status"))

        kid = d.pop("kid", UNSET)

        value_hash = d.pop("value_hash", UNSET)

        delivered_version = d.pop("delivered_version", UNSET)

        _last_delivery_attempt_at = d.pop("last_delivery_attempt_at", UNSET)
        last_delivery_attempt_at: datetime.datetime | Unset
        if isinstance(_last_delivery_attempt_at, Unset):
            last_delivery_attempt_at = UNSET
        else:
            last_delivery_attempt_at = datetime.datetime.fromisoformat(_last_delivery_attempt_at)

        _last_delivered_at = d.pop("last_delivered_at", UNSET)
        last_delivered_at: datetime.datetime | Unset
        if isinstance(_last_delivered_at, Unset):
            last_delivered_at = UNSET
        else:
            last_delivered_at = datetime.datetime.fromisoformat(_last_delivered_at)

        _last_delivery_error_code = d.pop("last_delivery_error_code", UNSET)
        last_delivery_error_code: ScopedAppSecretResponseLastDeliveryErrorCode | Unset
        if isinstance(_last_delivery_error_code, Unset):
            last_delivery_error_code = UNSET
        else:
            last_delivery_error_code = check_scoped_app_secret_response_last_delivery_error_code(
                _last_delivery_error_code
            )

        last_delivered_wake_id = d.pop("last_delivered_wake_id", UNSET)

        last_delivered_instance_id = d.pop("last_delivered_instance_id", UNSET)

        scoped_app_secret_response = cls(
            scope=scope,
            key=key,
            created_at=created_at,
            updated_at=updated_at,
            delivery_version=delivery_version,
            delivery_status=delivery_status,
            kid=kid,
            value_hash=value_hash,
            delivered_version=delivered_version,
            last_delivery_attempt_at=last_delivery_attempt_at,
            last_delivered_at=last_delivered_at,
            last_delivery_error_code=last_delivery_error_code,
            last_delivered_wake_id=last_delivered_wake_id,
            last_delivered_instance_id=last_delivered_instance_id,
        )

        scoped_app_secret_response.additional_properties = d
        return scoped_app_secret_response

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
