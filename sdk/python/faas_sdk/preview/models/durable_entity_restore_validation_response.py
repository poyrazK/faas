from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.durable_entity_restore_validation_response_isolation import (
    DurableEntityRestoreValidationResponseIsolation,
    check_durable_entity_restore_validation_response_isolation,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DurableEntityRestoreValidationResponse")


@_attrs_define
class DurableEntityRestoreValidationResponse:
    """Application validator verdict and deployment pin; does not reserve or commit entity state."""

    valid: bool
    deployment_id: UUID
    expected_version: int
    source_version: int
    bundle_sha256: str | Unset = UNSET
    isolation: DurableEntityRestoreValidationResponseIsolation | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        valid = self.valid

        deployment_id = str(self.deployment_id)

        expected_version = self.expected_version

        source_version = self.source_version

        bundle_sha256 = self.bundle_sha256

        isolation: str | Unset = UNSET
        if not isinstance(self.isolation, Unset):
            isolation = self.isolation

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "valid": valid,
                "deployment_id": deployment_id,
                "expected_version": expected_version,
                "source_version": source_version,
            }
        )
        if bundle_sha256 is not UNSET:
            field_dict["bundle_sha256"] = bundle_sha256
        if isolation is not UNSET:
            field_dict["isolation"] = isolation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        valid = d.pop("valid")

        deployment_id = UUID(d.pop("deployment_id"))

        expected_version = d.pop("expected_version")

        source_version = d.pop("source_version")

        bundle_sha256 = d.pop("bundle_sha256", UNSET)

        _isolation = d.pop("isolation", UNSET)
        isolation: DurableEntityRestoreValidationResponseIsolation | Unset
        if isinstance(_isolation, Unset):
            isolation = UNSET
        else:
            isolation = check_durable_entity_restore_validation_response_isolation(_isolation)

        durable_entity_restore_validation_response = cls(
            valid=valid,
            deployment_id=deployment_id,
            expected_version=expected_version,
            source_version=source_version,
            bundle_sha256=bundle_sha256,
            isolation=isolation,
        )

        return durable_entity_restore_validation_response
