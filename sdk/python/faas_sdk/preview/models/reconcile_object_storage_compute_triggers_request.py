from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.object_storage_compute_trigger import ObjectStorageComputeTrigger


T = TypeVar("T", bound="ReconcileObjectStorageComputeTriggersRequest")


@_attrs_define
class ReconcileObjectStorageComputeTriggersRequest:
    """Revision-checked replacement of only the bucket's declarative compute triggers."""

    expected_revision: int
    triggers: list[ObjectStorageComputeTrigger]

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        triggers = []
        for triggers_item_data in self.triggers:
            triggers_item = triggers_item_data.to_dict()
            triggers.append(triggers_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_revision": expected_revision,
                "triggers": triggers,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_storage_compute_trigger import ObjectStorageComputeTrigger

        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        triggers = []
        _triggers = d.pop("triggers")
        for triggers_item_data in _triggers:
            triggers_item = ObjectStorageComputeTrigger.from_dict(triggers_item_data)

            triggers.append(triggers_item)

        reconcile_object_storage_compute_triggers_request = cls(
            expected_revision=expected_revision,
            triggers=triggers,
        )

        return reconcile_object_storage_compute_triggers_request
