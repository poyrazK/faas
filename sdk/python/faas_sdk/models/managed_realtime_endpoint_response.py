from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_realtime_endpoint_response_auth_algorithms_item import (
    ManagedRealtimeEndpointResponseAuthAlgorithmsItem,
    check_managed_realtime_endpoint_response_auth_algorithms_item,
)
from ..models.managed_realtime_endpoint_response_auth_mode import (
    ManagedRealtimeEndpointResponseAuthMode,
    check_managed_realtime_endpoint_response_auth_mode,
)
from ..models.managed_realtime_endpoint_response_auth_token_masked import (
    ManagedRealtimeEndpointResponseAuthTokenMasked,
    check_managed_realtime_endpoint_response_auth_token_masked,
)
from ..models.managed_realtime_endpoint_response_callback_auth_token_masked import (
    ManagedRealtimeEndpointResponseCallbackAuthTokenMasked,
    check_managed_realtime_endpoint_response_callback_auth_token_masked,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_realtime_endpoint_response_auth_required_claims import (
        ManagedRealtimeEndpointResponseAuthRequiredClaims,
    )


T = TypeVar("T", bound="ManagedRealtimeEndpointResponse")


@_attrs_define
class ManagedRealtimeEndpointResponse:
    """Durable managed realtime endpoint configuration. Credentials are
    write-only and represented by a constant mask on every response.

    """

    id: UUID
    app_id: UUID
    account_id: UUID
    callback_url: str
    connect_path: str
    message_path: str
    disconnect_path: str
    callback_auth_token_masked: ManagedRealtimeEndpointResponseCallbackAuthTokenMasked
    auth_token_masked: ManagedRealtimeEndpointResponseAuthTokenMasked
    auth_mode: ManagedRealtimeEndpointResponseAuthMode
    allowed_origins: list[str]
    max_connections: int
    max_message_bytes: int
    max_connection_age_seconds: int
    enabled: bool
    created_at: datetime.datetime
    updated_at: datetime.datetime
    auth_token_previous_expires_at: datetime.datetime | None | Unset = UNSET
    """When the previous static bearer credential stops being accepted during rotation."""
    auth_issuer: str | Unset = UNSET
    auth_jwks_url: str | Unset = UNSET
    auth_audience: list[str] | Unset = UNSET
    auth_algorithms: list[ManagedRealtimeEndpointResponseAuthAlgorithmsItem] | Unset = UNSET
    auth_required_claims: ManagedRealtimeEndpointResponseAuthRequiredClaims | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        account_id = str(self.account_id)

        callback_url = self.callback_url

        connect_path = self.connect_path

        message_path = self.message_path

        disconnect_path = self.disconnect_path

        callback_auth_token_masked: str = self.callback_auth_token_masked

        auth_token_masked: str = self.auth_token_masked

        auth_mode: str = self.auth_mode

        allowed_origins = self.allowed_origins

        max_connections = self.max_connections

        max_message_bytes = self.max_message_bytes

        max_connection_age_seconds = self.max_connection_age_seconds

        enabled = self.enabled

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        auth_token_previous_expires_at: None | str | Unset
        if isinstance(self.auth_token_previous_expires_at, Unset):
            auth_token_previous_expires_at = UNSET
        elif isinstance(self.auth_token_previous_expires_at, datetime.datetime):
            auth_token_previous_expires_at = self.auth_token_previous_expires_at.isoformat()
        else:
            auth_token_previous_expires_at = self.auth_token_previous_expires_at

        auth_issuer = self.auth_issuer

        auth_jwks_url = self.auth_jwks_url

        auth_audience: list[str] | Unset = UNSET
        if not isinstance(self.auth_audience, Unset):
            auth_audience = self.auth_audience

        auth_algorithms: list[str] | Unset = UNSET
        if not isinstance(self.auth_algorithms, Unset):
            auth_algorithms = []
            for auth_algorithms_item_data in self.auth_algorithms:
                auth_algorithms_item: str = auth_algorithms_item_data
                auth_algorithms.append(auth_algorithms_item)

        auth_required_claims: dict[str, Any] | Unset = UNSET
        if not isinstance(self.auth_required_claims, Unset):
            auth_required_claims = self.auth_required_claims.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "account_id": account_id,
                "callback_url": callback_url,
                "connect_path": connect_path,
                "message_path": message_path,
                "disconnect_path": disconnect_path,
                "callback_auth_token_masked": callback_auth_token_masked,
                "auth_token_masked": auth_token_masked,
                "auth_mode": auth_mode,
                "allowed_origins": allowed_origins,
                "max_connections": max_connections,
                "max_message_bytes": max_message_bytes,
                "max_connection_age_seconds": max_connection_age_seconds,
                "enabled": enabled,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if auth_token_previous_expires_at is not UNSET:
            field_dict["auth_token_previous_expires_at"] = auth_token_previous_expires_at
        if auth_issuer is not UNSET:
            field_dict["auth_issuer"] = auth_issuer
        if auth_jwks_url is not UNSET:
            field_dict["auth_jwks_url"] = auth_jwks_url
        if auth_audience is not UNSET:
            field_dict["auth_audience"] = auth_audience
        if auth_algorithms is not UNSET:
            field_dict["auth_algorithms"] = auth_algorithms
        if auth_required_claims is not UNSET:
            field_dict["auth_required_claims"] = auth_required_claims

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_endpoint_response_auth_required_claims import (
            ManagedRealtimeEndpointResponseAuthRequiredClaims,
        )

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        account_id = UUID(d.pop("account_id"))

        callback_url = d.pop("callback_url")

        connect_path = d.pop("connect_path")

        message_path = d.pop("message_path")

        disconnect_path = d.pop("disconnect_path")

        callback_auth_token_masked = check_managed_realtime_endpoint_response_callback_auth_token_masked(
            d.pop("callback_auth_token_masked")
        )

        auth_token_masked = check_managed_realtime_endpoint_response_auth_token_masked(d.pop("auth_token_masked"))

        auth_mode = check_managed_realtime_endpoint_response_auth_mode(d.pop("auth_mode"))

        allowed_origins = cast(list[str], d.pop("allowed_origins"))

        max_connections = d.pop("max_connections")

        max_message_bytes = d.pop("max_message_bytes")

        max_connection_age_seconds = d.pop("max_connection_age_seconds")

        enabled = d.pop("enabled")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        def _parse_auth_token_previous_expires_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                auth_token_previous_expires_at_type_0 = datetime.datetime.fromisoformat(data)

                return auth_token_previous_expires_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        auth_token_previous_expires_at = _parse_auth_token_previous_expires_at(
            d.pop("auth_token_previous_expires_at", UNSET)
        )

        auth_issuer = d.pop("auth_issuer", UNSET)

        auth_jwks_url = d.pop("auth_jwks_url", UNSET)

        auth_audience = cast(list[str], d.pop("auth_audience", UNSET))

        _auth_algorithms = d.pop("auth_algorithms", UNSET)
        auth_algorithms: list[ManagedRealtimeEndpointResponseAuthAlgorithmsItem] | Unset = UNSET
        if _auth_algorithms is not UNSET:
            auth_algorithms = []
            for auth_algorithms_item_data in _auth_algorithms:
                auth_algorithms_item = check_managed_realtime_endpoint_response_auth_algorithms_item(
                    auth_algorithms_item_data
                )

                auth_algorithms.append(auth_algorithms_item)

        _auth_required_claims = d.pop("auth_required_claims", UNSET)
        auth_required_claims: ManagedRealtimeEndpointResponseAuthRequiredClaims | Unset
        if isinstance(_auth_required_claims, Unset):
            auth_required_claims = UNSET
        else:
            auth_required_claims = ManagedRealtimeEndpointResponseAuthRequiredClaims.from_dict(_auth_required_claims)

        managed_realtime_endpoint_response = cls(
            id=id,
            app_id=app_id,
            account_id=account_id,
            callback_url=callback_url,
            connect_path=connect_path,
            message_path=message_path,
            disconnect_path=disconnect_path,
            callback_auth_token_masked=callback_auth_token_masked,
            auth_token_masked=auth_token_masked,
            auth_mode=auth_mode,
            allowed_origins=allowed_origins,
            max_connections=max_connections,
            max_message_bytes=max_message_bytes,
            max_connection_age_seconds=max_connection_age_seconds,
            enabled=enabled,
            created_at=created_at,
            updated_at=updated_at,
            auth_token_previous_expires_at=auth_token_previous_expires_at,
            auth_issuer=auth_issuer,
            auth_jwks_url=auth_jwks_url,
            auth_audience=auth_audience,
            auth_algorithms=auth_algorithms,
            auth_required_claims=auth_required_claims,
        )

        managed_realtime_endpoint_response.additional_properties = d
        return managed_realtime_endpoint_response

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
