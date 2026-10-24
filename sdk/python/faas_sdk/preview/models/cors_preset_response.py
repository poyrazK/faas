from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CorsPresetResponse")


@_attrs_define
class CorsPresetResponse:
    """One cors_presets row (issue #975 #4 PR-B / ADR-129). The
    id is a server-generated UUID; app_id is null on the
    wire for account-wide presets and a UUID for app-scoped
    presets (the SQL NULL marker is the canonical "account-
    wide" encoding; the empty string is not used). The
    allow_origins array accepts the same CorsOriginPattern
    grammar as the inline EdgeRuleCORSAction field. The
    create-time gate enforces AllowCredentials: true +
    AllowOrigins: ["*"] ⇒ 422 (ADR-091 D12). Updated_at is
    bumped on every successful PATCH; the gateway's
    per-account overlay cache invalidates on pg_notify
    (cors_preset_changed).

    """

    id: UUID
    account_id: UUID
    name: str
    allow_origins: list[str]
    allow_methods: list[str]
    max_age_seconds: int
    created_at: datetime.datetime
    updated_at: datetime.datetime
    allow_credentials: bool = False
    app_id: None | Unset | UUID = UNSET
    """Optional app scoping (issue #975 #4 PR-B). Null on the
    wire = account-wide preset (visible to every app on
    the account). A UUID = app-scoped preset (visible
    only to that one app; cross-tenant IDOR collapses to
    404).
    """
    description: str | Unset = UNSET
    allow_headers: list[str] | Unset = UNSET
    expose_headers: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        account_id = str(self.account_id)

        name = self.name

        allow_origins = self.allow_origins

        allow_methods = self.allow_methods

        allow_credentials = self.allow_credentials

        max_age_seconds = self.max_age_seconds

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        app_id: None | str | Unset
        if isinstance(self.app_id, Unset):
            app_id = UNSET
        elif isinstance(self.app_id, UUID):
            app_id = str(self.app_id)
        else:
            app_id = self.app_id

        description = self.description

        allow_headers: list[str] | Unset = UNSET
        if not isinstance(self.allow_headers, Unset):
            allow_headers = self.allow_headers

        expose_headers: list[str] | Unset = UNSET
        if not isinstance(self.expose_headers, Unset):
            expose_headers = self.expose_headers

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "account_id": account_id,
                "name": name,
                "allow_origins": allow_origins,
                "allow_methods": allow_methods,
                "allow_credentials": allow_credentials,
                "max_age_seconds": max_age_seconds,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if description is not UNSET:
            field_dict["description"] = description
        if allow_headers is not UNSET:
            field_dict["allow_headers"] = allow_headers
        if expose_headers is not UNSET:
            field_dict["expose_headers"] = expose_headers

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        account_id = UUID(d.pop("account_id"))

        name = d.pop("name")

        allow_origins = cast(list[str], d.pop("allow_origins"))

        allow_methods = cast(list[str], d.pop("allow_methods"))

        allow_credentials = d.pop("allow_credentials")

        max_age_seconds = d.pop("max_age_seconds")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        def _parse_app_id(data: object) -> None | Unset | UUID:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                app_id_type_0 = UUID(data)

                return app_id_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | Unset | UUID, data)

        app_id = _parse_app_id(d.pop("app_id", UNSET))

        description = d.pop("description", UNSET)

        allow_headers = cast(list[str], d.pop("allow_headers", UNSET))

        expose_headers = cast(list[str], d.pop("expose_headers", UNSET))

        cors_preset_response = cls(
            id=id,
            account_id=account_id,
            name=name,
            allow_origins=allow_origins,
            allow_methods=allow_methods,
            allow_credentials=allow_credentials,
            max_age_seconds=max_age_seconds,
            created_at=created_at,
            updated_at=updated_at,
            app_id=app_id,
            description=description,
            allow_headers=allow_headers,
            expose_headers=expose_headers,
        )

        cors_preset_response.additional_properties = d
        return cors_preset_response

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
