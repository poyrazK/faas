from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.commit_routing import CommitRouting


T = TypeVar("T", bound="CommitEventRequest")


@_attrs_define
class CommitEventRequest:
    """At-least-once handoff. Repeating the identity with identical JSON data and routing returns the original receipt;
    changed type, schema version, data or routing conflicts.

    """

    id: UUID
    type_: str
    data: Any
    routing: CommitRouting | Unset = UNSET
    """Version 2 routing authority is granted by the account owner on the source. Customer identity is separate
    from untrusted event data. Business keys share a queue only within the same policy and customer scope; scalar
    types remain distinct. Canonical keys are limited to 256 bytes including the type prefix."""
    schema_version: str | Unset = UNSET
    """Optional registered application event schema version. Only event destinations support this field."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        type_ = self.type_

        data = self.data

        routing: dict[str, Any] | Unset = UNSET
        if not isinstance(self.routing, Unset):
            routing = self.routing.to_dict()

        schema_version = self.schema_version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "type": type_,
                "data": data,
            }
        )
        if routing is not UNSET:
            field_dict["routing"] = routing
        if schema_version is not UNSET:
            field_dict["schema_version"] = schema_version

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.commit_routing import CommitRouting

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        type_ = d.pop("type")

        data = d.pop("data")

        _routing = d.pop("routing", UNSET)
        routing: CommitRouting | Unset
        if isinstance(_routing, Unset):
            routing = UNSET
        else:
            routing = CommitRouting.from_dict(_routing)

        schema_version = d.pop("schema_version", UNSET)

        commit_event_request = cls(
            id=id,
            type_=type_,
            data=data,
            routing=routing,
            schema_version=schema_version,
        )

        return commit_event_request
