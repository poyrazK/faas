from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.api_sandbox_response import APISandboxResponse


T = TypeVar("T", bound="APISandboxInviteResponse")


@_attrs_define
class APISandboxInviteResponse:
    """Sandbox-specific bearer key and guide for using the isolated API."""

    sandbox: APISandboxResponse
    """Status and access details for a retained release's isolated API copy."""
    token: str
    """Sandbox-only bearer credential."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sandbox = self.sandbox.to_dict()

        token = self.token

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sandbox": sandbox,
                "token": token,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_sandbox_response import APISandboxResponse

        d = dict(src_dict)
        sandbox = APISandboxResponse.from_dict(d.pop("sandbox"))

        token = d.pop("token")

        api_sandbox_invite_response = cls(
            sandbox=sandbox,
            token=token,
        )

        api_sandbox_invite_response.additional_properties = d
        return api_sandbox_invite_response

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
