from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_activity_hint_code import (
    ManagedPostgresActivityHintCode,
    check_managed_postgres_activity_hint_code,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresActivityHint")


@_attrs_define
class ManagedPostgresActivityHint:
    """A suggestion for a cause that keeps the database awake."""

    code: ManagedPostgresActivityHintCode
    cause: str
    message: str
    app_slug: str | Unset = UNSET
    command: str | Unset = UNSET
    """ The CLI command that acts on the hint, when there is one. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        code: str = self.code

        cause = self.cause

        message = self.message

        app_slug = self.app_slug

        command = self.command

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "code": code,
                "cause": cause,
                "message": message,
            }
        )
        if app_slug is not UNSET:
            field_dict["app_slug"] = app_slug
        if command is not UNSET:
            field_dict["command"] = command

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = check_managed_postgres_activity_hint_code(d.pop("code"))

        cause = d.pop("cause")

        message = d.pop("message")

        app_slug = d.pop("app_slug", UNSET)

        command = d.pop("command", UNSET)

        managed_postgres_activity_hint = cls(
            code=code,
            cause=cause,
            message=message,
            app_slug=app_slug,
            command=command,
        )

        managed_postgres_activity_hint.additional_properties = d
        return managed_postgres_activity_hint

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
