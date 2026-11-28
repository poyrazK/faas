from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_accounting_diagnostic_reasons_item import (
    ManagedPostgresAccountingDiagnosticReasonsItem,
    check_managed_postgres_accounting_diagnostic_reasons_item,
)
from ..models.managed_postgres_accounting_diagnostic_state import (
    ManagedPostgresAccountingDiagnosticState,
    check_managed_postgres_accounting_diagnostic_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresAccountingDiagnostic")


@_attrs_define
class ManagedPostgresAccountingDiagnostic:
    """Local evidence for one accountable database. Shared restores refer to their accounting root. Reasons explain stale
    admission only; an empty list does not establish invoice settlement or remaining budget. An unknown legacy tombstone
    has no confirmed terminal deadline.

    """

    database_id: UUID
    name: str
    state: ManagedPostgresAccountingDiagnosticState
    accounting_required: bool
    identity_known: bool
    accounting_database_id: UUID
    blocking: bool
    """This resource blocks admission due to stale accounting under the enabled policy. False when the policy is
    disabled."""
    reasons: list[ManagedPostgresAccountingDiagnosticReasonsItem]
    collected_window_seconds: int
    required_from: datetime.datetime | None | Unset = UNSET
    required_until: datetime.datetime | None | Unset = UNSET
    collected_from: datetime.datetime | None | Unset = UNSET
    collected_until: datetime.datetime | None | Unset = UNSET
    observed_at: datetime.datetime | None | Unset = UNSET
    correction_observed_at: datetime.datetime | None | Unset = UNSET
    correction_required_at: datetime.datetime | None | Unset = UNSET
    lease_until: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        database_id = str(self.database_id)

        name = self.name

        state: str = self.state

        accounting_required = self.accounting_required

        identity_known = self.identity_known

        accounting_database_id = str(self.accounting_database_id)

        blocking = self.blocking

        reasons = []
        for reasons_item_data in self.reasons:
            reasons_item: str = reasons_item_data
            reasons.append(reasons_item)

        collected_window_seconds = self.collected_window_seconds

        required_from: None | str | Unset
        if isinstance(self.required_from, Unset):
            required_from = UNSET
        elif isinstance(self.required_from, datetime.datetime):
            required_from = self.required_from.isoformat()
        else:
            required_from = self.required_from

        required_until: None | str | Unset
        if isinstance(self.required_until, Unset):
            required_until = UNSET
        elif isinstance(self.required_until, datetime.datetime):
            required_until = self.required_until.isoformat()
        else:
            required_until = self.required_until

        collected_from: None | str | Unset
        if isinstance(self.collected_from, Unset):
            collected_from = UNSET
        elif isinstance(self.collected_from, datetime.datetime):
            collected_from = self.collected_from.isoformat()
        else:
            collected_from = self.collected_from

        collected_until: None | str | Unset
        if isinstance(self.collected_until, Unset):
            collected_until = UNSET
        elif isinstance(self.collected_until, datetime.datetime):
            collected_until = self.collected_until.isoformat()
        else:
            collected_until = self.collected_until

        observed_at: None | str | Unset
        if isinstance(self.observed_at, Unset):
            observed_at = UNSET
        elif isinstance(self.observed_at, datetime.datetime):
            observed_at = self.observed_at.isoformat()
        else:
            observed_at = self.observed_at

        correction_observed_at: None | str | Unset
        if isinstance(self.correction_observed_at, Unset):
            correction_observed_at = UNSET
        elif isinstance(self.correction_observed_at, datetime.datetime):
            correction_observed_at = self.correction_observed_at.isoformat()
        else:
            correction_observed_at = self.correction_observed_at

        correction_required_at: None | str | Unset
        if isinstance(self.correction_required_at, Unset):
            correction_required_at = UNSET
        elif isinstance(self.correction_required_at, datetime.datetime):
            correction_required_at = self.correction_required_at.isoformat()
        else:
            correction_required_at = self.correction_required_at

        lease_until: None | str | Unset
        if isinstance(self.lease_until, Unset):
            lease_until = UNSET
        elif isinstance(self.lease_until, datetime.datetime):
            lease_until = self.lease_until.isoformat()
        else:
            lease_until = self.lease_until

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "database_id": database_id,
                "name": name,
                "state": state,
                "accounting_required": accounting_required,
                "identity_known": identity_known,
                "accounting_database_id": accounting_database_id,
                "blocking": blocking,
                "reasons": reasons,
                "collected_window_seconds": collected_window_seconds,
            }
        )
        if required_from is not UNSET:
            field_dict["required_from"] = required_from
        if required_until is not UNSET:
            field_dict["required_until"] = required_until
        if collected_from is not UNSET:
            field_dict["collected_from"] = collected_from
        if collected_until is not UNSET:
            field_dict["collected_until"] = collected_until
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at
        if correction_observed_at is not UNSET:
            field_dict["correction_observed_at"] = correction_observed_at
        if correction_required_at is not UNSET:
            field_dict["correction_required_at"] = correction_required_at
        if lease_until is not UNSET:
            field_dict["lease_until"] = lease_until

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        database_id = UUID(d.pop("database_id"))

        name = d.pop("name")

        state = check_managed_postgres_accounting_diagnostic_state(d.pop("state"))

        accounting_required = d.pop("accounting_required")

        identity_known = d.pop("identity_known")

        accounting_database_id = UUID(d.pop("accounting_database_id"))

        blocking = d.pop("blocking")

        reasons = []
        _reasons = d.pop("reasons")
        for reasons_item_data in _reasons:
            reasons_item = check_managed_postgres_accounting_diagnostic_reasons_item(reasons_item_data)

            reasons.append(reasons_item)

        collected_window_seconds = d.pop("collected_window_seconds")

        def _parse_required_from(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                required_from_type_0 = datetime.datetime.fromisoformat(data)

                return required_from_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        required_from = _parse_required_from(d.pop("required_from", UNSET))

        def _parse_required_until(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                required_until_type_0 = datetime.datetime.fromisoformat(data)

                return required_until_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        required_until = _parse_required_until(d.pop("required_until", UNSET))

        def _parse_collected_from(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                collected_from_type_0 = datetime.datetime.fromisoformat(data)

                return collected_from_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        collected_from = _parse_collected_from(d.pop("collected_from", UNSET))

        def _parse_collected_until(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                collected_until_type_0 = datetime.datetime.fromisoformat(data)

                return collected_until_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        collected_until = _parse_collected_until(d.pop("collected_until", UNSET))

        def _parse_observed_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                observed_at_type_0 = datetime.datetime.fromisoformat(data)

                return observed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        observed_at = _parse_observed_at(d.pop("observed_at", UNSET))

        def _parse_correction_observed_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                correction_observed_at_type_0 = datetime.datetime.fromisoformat(data)

                return correction_observed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        correction_observed_at = _parse_correction_observed_at(d.pop("correction_observed_at", UNSET))

        def _parse_correction_required_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                correction_required_at_type_0 = datetime.datetime.fromisoformat(data)

                return correction_required_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        correction_required_at = _parse_correction_required_at(d.pop("correction_required_at", UNSET))

        def _parse_lease_until(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                lease_until_type_0 = datetime.datetime.fromisoformat(data)

                return lease_until_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        lease_until = _parse_lease_until(d.pop("lease_until", UNSET))

        managed_postgres_accounting_diagnostic = cls(
            database_id=database_id,
            name=name,
            state=state,
            accounting_required=accounting_required,
            identity_known=identity_known,
            accounting_database_id=accounting_database_id,
            blocking=blocking,
            reasons=reasons,
            collected_window_seconds=collected_window_seconds,
            required_from=required_from,
            required_until=required_until,
            collected_from=collected_from,
            collected_until=collected_until,
            observed_at=observed_at,
            correction_observed_at=correction_observed_at,
            correction_required_at=correction_required_at,
            lease_until=lease_until,
        )

        managed_postgres_accounting_diagnostic.additional_properties = d
        return managed_postgres_accounting_diagnostic

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
