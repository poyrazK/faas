from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeRuleCORSAction")


@_attrs_define
class EdgeRuleCORSAction:
    """Stamps CORS headers + handles preflight in-process."""

    allow_origins: list[str]
    allow_methods: list[str]
    cors_preset_id: None | Unset | UUID = UNSET
    """Optional CORS preset reference (issue #975 #4 PR-B /
    ADR-129). When set, the rule's resolved CORS action is
    the merged union of the preset's fields and the rule's
    inline fields — with the rule taking precedence for any
    non-empty inline field. Mutually exclusive with inline
    fields: if cors_preset_id is set, allow_origins,
    allow_methods, allow_headers, expose_headers,
    allow_credentials, and max_age_seconds must all be empty
    / unset. The preset is referenced by id (UUID); an
    invalid id (cross-tenant, deleted) causes the rule to
    be silently dropped from the gateway's compiled slice
    (the request matches no rule, returning 404 from the
    route layer).
    """
    allow_headers: list[str] | Unset = UNSET
    expose_headers: list[str] | Unset = UNSET
    allow_credentials: bool | Unset = False
    max_age_seconds: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        allow_origins = self.allow_origins

        allow_methods = self.allow_methods

        cors_preset_id: None | str | Unset
        if isinstance(self.cors_preset_id, Unset):
            cors_preset_id = UNSET
        elif isinstance(self.cors_preset_id, UUID):
            cors_preset_id = str(self.cors_preset_id)
        else:
            cors_preset_id = self.cors_preset_id

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
        field_dict.update(
            {
                "allow_origins": allow_origins,
                "allow_methods": allow_methods,
            }
        )
        if cors_preset_id is not UNSET:
            field_dict["cors_preset_id"] = cors_preset_id
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
        allow_origins = cast(list[str], d.pop("allow_origins"))

        allow_methods = cast(list[str], d.pop("allow_methods"))

        def _parse_cors_preset_id(data: object) -> None | Unset | UUID:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                cors_preset_id_type_0 = UUID(data)

                return cors_preset_id_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | Unset | UUID, data)

        cors_preset_id = _parse_cors_preset_id(d.pop("cors_preset_id", UNSET))

        allow_headers = cast(list[str], d.pop("allow_headers", UNSET))

        expose_headers = cast(list[str], d.pop("expose_headers", UNSET))

        allow_credentials = d.pop("allow_credentials", UNSET)

        max_age_seconds = d.pop("max_age_seconds", UNSET)

        edge_rule_cors_action = cls(
            allow_origins=allow_origins,
            allow_methods=allow_methods,
            cors_preset_id=cors_preset_id,
            allow_headers=allow_headers,
            expose_headers=expose_headers,
            allow_credentials=allow_credentials,
            max_age_seconds=max_age_seconds,
        )

        edge_rule_cors_action.additional_properties = d
        return edge_rule_cors_action

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
