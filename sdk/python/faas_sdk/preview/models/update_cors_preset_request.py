from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="UpdateCorsPresetRequest")


@_attrs_define
class UpdateCorsPresetRequest:
    """Body for PATCH /v1/cors-presets/{id}. Every field is
    optional (PATCH nil-skip convention). At least one field
    must be present (an empty PATCH is rejected with 422
    cors_preset_update_requires_field). The same partial
    grammar check that fires on CreateCorsPresetRequest
    (CorsOriginPattern, *+credentials footgun) fires here
    on the partial payload; the apid handler additionally
    re-validates against the merged post-update shape so a
    PATCH that flips AllowCredentials to true while leaving
    AllowOrigins=["*"] is rejected.

    app_id uses the **string tri-state: outer null = "do
    not touch", inner null = "set to NULL (account-wide)",
    inner non-null = "set to UUID (app-scoped)".

    """

    app_id: None | Unset | UUID = UNSET
    """Optional app scoping. Outer null = do not touch.
    Inner null = set to NULL (account-wide). Inner
    non-null = set to UUID (app-scoped).
    """
    name: str | Unset = UNSET
    description: str | Unset = UNSET
    allow_origins: list[str] | Unset = UNSET
    allow_methods: list[str] | Unset = UNSET
    allow_headers: list[str] | Unset = UNSET
    expose_headers: list[str] | Unset = UNSET
    allow_credentials: bool | Unset = UNSET
    max_age_seconds: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id: None | str | Unset
        if isinstance(self.app_id, Unset):
            app_id = UNSET
        elif isinstance(self.app_id, UUID):
            app_id = str(self.app_id)
        else:
            app_id = self.app_id

        name = self.name

        description = self.description

        allow_origins: list[str] | Unset = UNSET
        if not isinstance(self.allow_origins, Unset):
            allow_origins = self.allow_origins

        allow_methods: list[str] | Unset = UNSET
        if not isinstance(self.allow_methods, Unset):
            allow_methods = self.allow_methods

        allow_headers: list[str] | Unset = UNSET
        if not isinstance(self.allow_headers, Unset):
            allow_headers = self.allow_headers

        expose_headers: list[str] | Unset = UNSET
        if not isinstance(self.expose_headers, Unset):
            expose_headers = self.expose_headers

        allow_credentials = self.allow_credentials

        max_age_seconds = self.max_age_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if name is not UNSET:
            field_dict["name"] = name
        if description is not UNSET:
            field_dict["description"] = description
        if allow_origins is not UNSET:
            field_dict["allow_origins"] = allow_origins
        if allow_methods is not UNSET:
            field_dict["allow_methods"] = allow_methods
        if allow_headers is not UNSET:
            field_dict["allow_headers"] = allow_headers
        if expose_headers is not UNSET:
            field_dict["expose_headers"] = expose_headers
        if allow_credentials is not UNSET:
            field_dict["allow_credentials"] = allow_credentials
        if max_age_seconds is not UNSET:
            field_dict["max_age_seconds"] = max_age_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)

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

        name = d.pop("name", UNSET)

        description = d.pop("description", UNSET)

        allow_origins = cast(list[str], d.pop("allow_origins", UNSET))

        allow_methods = cast(list[str], d.pop("allow_methods", UNSET))

        allow_headers = cast(list[str], d.pop("allow_headers", UNSET))

        expose_headers = cast(list[str], d.pop("expose_headers", UNSET))

        allow_credentials = d.pop("allow_credentials", UNSET)

        max_age_seconds = d.pop("max_age_seconds", UNSET)

        update_cors_preset_request = cls(
            app_id=app_id,
            name=name,
            description=description,
            allow_origins=allow_origins,
            allow_methods=allow_methods,
            allow_headers=allow_headers,
            expose_headers=expose_headers,
            allow_credentials=allow_credentials,
            max_age_seconds=max_age_seconds,
        )

        update_cors_preset_request.additional_properties = d
        return update_cors_preset_request

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
