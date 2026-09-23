from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_secret_response_delivery_status import (
    AppSecretResponseDeliveryStatus,
    check_app_secret_response_delivery_status,
)
from ..models.app_secret_response_last_delivery_error_code import (
    AppSecretResponseLastDeliveryErrorCode,
    check_app_secret_response_last_delivery_error_code,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AppSecretResponse")


@_attrs_define
class AppSecretResponse:
    """A sealed secret envelope: key name, sealed ciphertext (server can't read it), version, and timestamps. Scope is the
    env-scope the row belongs to (ADR-092 PR-B). Pre-PR-B callers see scope='default' echoed on every row.

    """

    key: str
    scope: str
    created_at: datetime.datetime
    updated_at: datetime.datetime
    delivery_version: int
    """Opaque monotonic version of the runtime value. Advances on value mutation, not host-key reseal."""
    delivery_status: AppSecretResponseDeliveryStatus
    """Delivery state for the current delivery_version. A concurrent rotation remains pending until that exact
    version starts successfully."""
    kid: str | Unset = UNSET
    """age-1... recipient string of the host identity that sealed this row (ADR-089). Empty for rows sealed before
    migration 00166."""
    value_hash: str | Unset = UNSET
    """16-hex HMAC-SHA256(plaintext) keyed by the per-host host.hmac.key (ADR-117 PR-C). Empty for pre-PR-C rows.
    Same value across scopes = byte-identical plaintext."""
    delivered_version: int | Unset = UNSET
    """Newest version confirmed in a successfully started runtime. Omitted until the first successful delivery."""
    last_delivery_attempt_at: datetime.datetime | Unset = UNSET
    last_delivered_at: datetime.datetime | Unset = UNSET
    last_delivery_error_code: AppSecretResponseLastDeliveryErrorCode | Unset = UNSET
    """Non-sensitive closed failure reason for the current version; omitted unless delivery_status is failed."""
    last_delivered_wake_id: str | Unset = UNSET
    """Wake correlation id of the most recent successful delivery."""
    last_delivered_instance_id: str | Unset = UNSET
    """Runtime instance that most recently received a secret version."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        scope = self.scope

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
                "key": key,
                "scope": scope,
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
        key = d.pop("key")

        scope = d.pop("scope")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        delivery_version = d.pop("delivery_version")

        delivery_status = check_app_secret_response_delivery_status(d.pop("delivery_status"))

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
        last_delivery_error_code: AppSecretResponseLastDeliveryErrorCode | Unset
        if isinstance(_last_delivery_error_code, Unset):
            last_delivery_error_code = UNSET
        else:
            last_delivery_error_code = check_app_secret_response_last_delivery_error_code(_last_delivery_error_code)

        last_delivered_wake_id = d.pop("last_delivered_wake_id", UNSET)

        last_delivered_instance_id = d.pop("last_delivered_instance_id", UNSET)

        app_secret_response = cls(
            key=key,
            scope=scope,
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

        app_secret_response.additional_properties = d
        return app_secret_response

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
