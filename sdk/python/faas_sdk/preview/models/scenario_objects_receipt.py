from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_objects_receipt_version import (
    ScenarioObjectsReceiptVersion,
    check_scenario_objects_receipt_version,
)

T = TypeVar("T", bound="ScenarioObjectsReceipt")


@_attrs_define
class ScenarioObjectsReceipt:
    """Immutable storage enrollment acknowledgement containing neither content nor credentials."""

    collector_id: UUID
    """Private listener incarnation that acknowledged the namespace before deployment."""
    version: ScenarioObjectsReceiptVersion
    run_id: str
    app_id: UUID
    resource_id: UUID
    fixture_sha256: str
    contract_sha256: str
    initial_sha256: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        collector_id = str(self.collector_id)

        version: int = self.version

        run_id = self.run_id

        app_id = str(self.app_id)

        resource_id = str(self.resource_id)

        fixture_sha256 = self.fixture_sha256

        contract_sha256 = self.contract_sha256

        initial_sha256 = self.initial_sha256

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "collector_id": collector_id,
                "version": version,
                "run_id": run_id,
                "app_id": app_id,
                "resource_id": resource_id,
                "fixture_sha256": fixture_sha256,
                "contract_sha256": contract_sha256,
                "initial_sha256": initial_sha256,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        collector_id = UUID(d.pop("collector_id"))

        version = check_scenario_objects_receipt_version(d.pop("version"))

        run_id = d.pop("run_id")

        app_id = UUID(d.pop("app_id"))

        resource_id = UUID(d.pop("resource_id"))

        fixture_sha256 = d.pop("fixture_sha256")

        contract_sha256 = d.pop("contract_sha256")

        initial_sha256 = d.pop("initial_sha256")

        scenario_objects_receipt = cls(
            collector_id=collector_id,
            version=version,
            run_id=run_id,
            app_id=app_id,
            resource_id=resource_id,
            fixture_sha256=fixture_sha256,
            contract_sha256=contract_sha256,
            initial_sha256=initial_sha256,
        )

        scenario_objects_receipt.additional_properties = d
        return scenario_objects_receipt

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
