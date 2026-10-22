from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="DevWatch")


@_attrs_define
class DevWatch:
    """Watch mode (ADR-1170). The environment runs this development server instead of the production build, and live source
    patches reach the server's own reloader. Omit to turn watch mode off; the change applies from the next developer
    build. Refused with 422 dev_watch_unsupported when the developer app has less than 512 MB of RAM or does not require
    authentication.

    """

    command: str
    """One-line command that starts the development server, for example "npm run dev"."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        command = self.command

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "command": command,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        command = d.pop("command")

        dev_watch = cls(
            command=command,
        )

        dev_watch.additional_properties = d
        return dev_watch

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
