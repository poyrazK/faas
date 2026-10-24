from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateCorsPresetRequest")


@_attrs_define
class CreateCorsPresetRequest:
    """Body for POST /v1/cors-presets. The customer must supply
    at least one allow_origin and one allow_method. AppID is
    optional on the wire (null pointer = "account-wide",
    non-nil = "app-scoped"); the handler maps the
    pointer-nil case to a SQL NULL on insert. Name length
    is 1..64 characters (cors_presets_name_check). The
    *+credentials footgun (ADR-091 D12) is enforced at
    validate-time.

    """

    name: str
    allow_origins: list[str]
    allow_methods: list[str]
    max_age_seconds: int
    allow_credentials: bool = False
    app_id: None | Unset | UUID = UNSET
    """Optional app scoping. Null = account-wide. Set to a
    UUID = app-scoped.
    """
    description: str | Unset = UNSET
    allow_headers: list[str] | Unset = UNSET
    expose_headers: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        allow_origins = self.allow_origins

        allow_methods = self.allow_methods

        allow_credentials = self.allow_credentials

        max_age_seconds = self.max_age_seconds

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
                "name": name,
                "allow_origins": allow_origins,
                "allow_methods": allow_methods,
                "allow_credentials": allow_credentials,
                "max_age_seconds": max_age_seconds,
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
        name = d.pop("name")

        allow_origins = cast(list[str], d.pop("allow_origins"))

        allow_methods = cast(list[str], d.pop("allow_methods"))

        allow_credentials = d.pop("allow_credentials")

        max_age_seconds = d.pop("max_age_seconds")

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

        create_cors_preset_request = cls(
            name=name,
            allow_origins=allow_origins,
            allow_methods=allow_methods,
            allow_credentials=allow_credentials,
            max_age_seconds=max_age_seconds,
            app_id=app_id,
            description=description,
            allow_headers=allow_headers,
            expose_headers=expose_headers,
        )

        create_cors_preset_request.additional_properties = d
        return create_cors_preset_request

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
