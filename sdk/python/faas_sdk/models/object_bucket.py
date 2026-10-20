from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.object_bucket_state import ObjectBucketState, check_object_bucket_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObjectBucket")


@_attrs_define
class ObjectBucket:
    """Logical bucket metadata without upstream credentials or placement details."""

    id: UUID
    name: str
    scope: str
    region: str
    state: ObjectBucketState
    public: bool
    created_at: datetime.datetime
    serve_at: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        scope = self.scope

        region = self.region

        state: str = self.state

        public = self.public

        created_at = self.created_at.isoformat()

        serve_at = self.serve_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "scope": scope,
                "region": region,
                "state": state,
                "public": public,
                "created_at": created_at,
            }
        )
        if serve_at is not UNSET:
            field_dict["serve_at"] = serve_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        scope = d.pop("scope")

        region = d.pop("region")

        state = check_object_bucket_state(d.pop("state"))

        public = d.pop("public")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        serve_at = d.pop("serve_at", UNSET)

        object_bucket = cls(
            id=id,
            name=name,
            scope=scope,
            region=region,
            state=state,
            public=public,
            created_at=created_at,
            serve_at=serve_at,
        )

        object_bucket.additional_properties = d
        return object_bucket

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
