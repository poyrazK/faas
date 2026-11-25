from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AnonymousSessionRequest")


@_attrs_define
class AnonymousSessionRequest:
    """Body for `POST /v1/anonymous/sessions`."""

    accept_terms: bool
    """Must be true: deploying without an account accepts the Terms of Service."""
    client: str | Unset = UNSET
    """The tool creating the session, such as gregale-cli."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        accept_terms = self.accept_terms

        client = self.client

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "accept_terms": accept_terms,
            }
        )
        if client is not UNSET:
            field_dict["client"] = client

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        accept_terms = d.pop("accept_terms")

        client = d.pop("client", UNSET)

        anonymous_session_request = cls(
            accept_terms=accept_terms,
            client=client,
        )

        anonymous_session_request.additional_properties = d
        return anonymous_session_request

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
