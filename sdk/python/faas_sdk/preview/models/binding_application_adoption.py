from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.binding_application_adoption_source import (
    BindingApplicationAdoptionSource,
    check_binding_application_adoption_source,
)
from ..models.binding_application_adoption_status import (
    BindingApplicationAdoptionStatus,
    check_binding_application_adoption_status,
)

if TYPE_CHECKING:
    from ..models.binding_adoption_counts import BindingAdoptionCounts
    from ..models.binding_application_ack_target import BindingApplicationAckTarget


T = TypeVar("T", bound="BindingApplicationAdoption")


@_attrs_define
class BindingApplicationAdoption:
    """Metadata-only application self-attestations for managed PostgreSQL or object-storage secrets. Counts refer to
    workload/secret pairs. Reads use a single state snapshot and include missing reports. Task guests, jobs, mirrors and
    unauthorized workloads are excluded. Receipts expire when secret versions change, independently of probe age. An
    older guest projection does not erase a newer application receipt. complete describes this optional observation
    read; a read failure leaves default checks unchanged and blocks strict checks.

    """

    source: BindingApplicationAdoptionSource
    status: BindingApplicationAdoptionStatus
    observed_at: datetime.datetime
    complete: bool
    secrets_expected: int
    secrets_observed: int
    reload: BindingAdoptionCounts
    """Counts of authorized workload/secret pairs, derived from versioned receipts."""
    application: BindingAdoptionCounts
    """Counts of authorized workload/secret pairs, derived from versioned receipts."""
    targets: list[BindingApplicationAckTarget]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source: str = self.source

        status: str = self.status

        observed_at = self.observed_at.isoformat()

        complete = self.complete

        secrets_expected = self.secrets_expected

        secrets_observed = self.secrets_observed

        reload = self.reload.to_dict()

        application = self.application.to_dict()

        targets = []
        for targets_item_data in self.targets:
            targets_item = targets_item_data.to_dict()
            targets.append(targets_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
                "status": status,
                "observed_at": observed_at,
                "complete": complete,
                "secrets_expected": secrets_expected,
                "secrets_observed": secrets_observed,
                "reload": reload,
                "application": application,
                "targets": targets,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.binding_adoption_counts import BindingAdoptionCounts
        from ..models.binding_application_ack_target import BindingApplicationAckTarget

        d = dict(src_dict)
        source = check_binding_application_adoption_source(d.pop("source"))

        status = check_binding_application_adoption_status(d.pop("status"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        complete = d.pop("complete")

        secrets_expected = d.pop("secrets_expected")

        secrets_observed = d.pop("secrets_observed")

        reload = BindingAdoptionCounts.from_dict(d.pop("reload"))

        application = BindingAdoptionCounts.from_dict(d.pop("application"))

        targets = []
        _targets = d.pop("targets")
        for targets_item_data in _targets:
            targets_item = BindingApplicationAckTarget.from_dict(targets_item_data)

            targets.append(targets_item)

        binding_application_adoption = cls(
            source=source,
            status=status,
            observed_at=observed_at,
            complete=complete,
            secrets_expected=secrets_expected,
            secrets_observed=secrets_observed,
            reload=reload,
            application=application,
            targets=targets,
        )

        binding_application_adoption.additional_properties = d
        return binding_application_adoption

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
