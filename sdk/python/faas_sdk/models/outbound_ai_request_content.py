from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.outbound_ai_content_body import OutboundAIContentBody


T = TypeVar("T", bound="OutboundAIRequestContent")


@_attrs_define
class OutboundAIRequestContent:
    """One logged AI call's captured bodies as the gateway sent them to the provider and read them back; never headers
    (ADR-996). Personal data and secrets are redacted (ADR-997).

    """

    request_id: UUID
    created_at: datetime.datetime
    request_path: None | str
    """The provider path of the call; null for captures made before ADR-997."""
    request: OutboundAIContentBody
    """The start of one captured body."""
    response: OutboundAIContentBody
    """The start of one captured body."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        created_at = self.created_at.isoformat()

        request_path: None | str
        request_path = self.request_path

        request = self.request.to_dict()

        response = self.response.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request_id": request_id,
                "created_at": created_at,
                "request_path": request_path,
                "request": request,
                "response": response,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_content_body import OutboundAIContentBody

        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        def _parse_request_path(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        request_path = _parse_request_path(d.pop("request_path"))

        request = OutboundAIContentBody.from_dict(d.pop("request"))

        response = OutboundAIContentBody.from_dict(d.pop("response"))

        outbound_ai_request_content = cls(
            request_id=request_id,
            created_at=created_at,
            request_path=request_path,
            request=request,
            response=response,
        )

        outbound_ai_request_content.additional_properties = d
        return outbound_ai_request_content

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
