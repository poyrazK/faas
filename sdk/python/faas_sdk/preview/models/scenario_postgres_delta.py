from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.scenario_postgres_delta_deleted_item import ScenarioPostgresDeltaDeletedItem
    from ..models.scenario_postgres_delta_inserted_item import ScenarioPostgresDeltaInsertedItem


T = TypeVar("T", bound="ScenarioPostgresDelta")


@_attrs_define
class ScenarioPostgresDelta:
    """Inserted and deleted selected-row multisets; updates appear as deletion and insertion."""

    table: str
    inserted: list[ScenarioPostgresDeltaInsertedItem]
    deleted: list[ScenarioPostgresDeltaDeletedItem]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        table = self.table

        inserted = []
        for inserted_item_data in self.inserted:
            inserted_item = inserted_item_data.to_dict()
            inserted.append(inserted_item)

        deleted = []
        for deleted_item_data in self.deleted:
            deleted_item = deleted_item_data.to_dict()
            deleted.append(deleted_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "table": table,
                "inserted": inserted,
                "deleted": deleted,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scenario_postgres_delta_deleted_item import ScenarioPostgresDeltaDeletedItem
        from ..models.scenario_postgres_delta_inserted_item import ScenarioPostgresDeltaInsertedItem

        d = dict(src_dict)
        table = d.pop("table")

        inserted = []
        _inserted = d.pop("inserted")
        for inserted_item_data in _inserted:
            inserted_item = ScenarioPostgresDeltaInsertedItem.from_dict(inserted_item_data)

            inserted.append(inserted_item)

        deleted = []
        _deleted = d.pop("deleted")
        for deleted_item_data in _deleted:
            deleted_item = ScenarioPostgresDeltaDeletedItem.from_dict(deleted_item_data)

            deleted.append(deleted_item)

        scenario_postgres_delta = cls(
            table=table,
            inserted=inserted,
            deleted=deleted,
        )

        scenario_postgres_delta.additional_properties = d
        return scenario_postgres_delta

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
