from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.publish_event_response import PublishEventResponse


T = TypeVar("T", bound="AppPublishEventResponse")


@_attrs_define
class AppPublishEventResponse:
    """Durable app producer-key publication decision."""

    app_id: UUID
    source: str
    """Stable app.UUID subscription source."""
    duplicate: bool
    receipt: PublishEventResponse
    """Durable acceptance receipt for a published internal event."""

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        source = self.source

        duplicate = self.duplicate

        receipt = self.receipt.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "source": source,
                "duplicate": duplicate,
                "receipt": receipt,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.publish_event_response import PublishEventResponse

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        source = d.pop("source")

        duplicate = d.pop("duplicate")

        receipt = PublishEventResponse.from_dict(d.pop("receipt"))

        app_publish_event_response = cls(
            app_id=app_id,
            source=source,
            duplicate=duplicate,
            receipt=receipt,
        )

        return app_publish_event_response
