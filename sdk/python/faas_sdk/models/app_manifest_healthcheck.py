from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AppManifestHealthcheck")


@_attrs_define
class AppManifestHealthcheck:
    """AppManifest-level projection of the OCI HEALTHCHECK shape (ADR-136 §Decision 3-4). Durations are integer seconds at
    the JSON boundary to match OCI/Docker conventions. Runtime polling lands in M-2 (ADR-X5); M-1 surfaces the field for
    the registry-pull path.

    """

    test: list[str] | Unset = UNSET
    """Argv of the check command, prefixed by "CMD", "CMD-SHELL", or "NONE" per Docker semantics."""
    interval_s: int | None | Unset = UNSET
    """Poll cadence after StartPeriodS elapses (Docker default 30s)."""
    timeout_s: int | None | Unset = UNSET
    """Per-probe exec timeout (Docker default 30s)."""
    retries: int | None | Unset = UNSET
    """Consecutive failure count to mark unhealthy (Docker default 3)."""
    start_period_s: int | None | Unset = UNSET
    """Startup grace during which failures don't count (Docker 17.05+, default 0s)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        test: list[str] | Unset = UNSET
        if not isinstance(self.test, Unset):
            test = self.test

        interval_s: int | None | Unset
        if isinstance(self.interval_s, Unset):
            interval_s = UNSET
        else:
            interval_s = self.interval_s

        timeout_s: int | None | Unset
        if isinstance(self.timeout_s, Unset):
            timeout_s = UNSET
        else:
            timeout_s = self.timeout_s

        retries: int | None | Unset
        if isinstance(self.retries, Unset):
            retries = UNSET
        else:
            retries = self.retries

        start_period_s: int | None | Unset
        if isinstance(self.start_period_s, Unset):
            start_period_s = UNSET
        else:
            start_period_s = self.start_period_s

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if test is not UNSET:
            field_dict["test"] = test
        if interval_s is not UNSET:
            field_dict["interval_s"] = interval_s
        if timeout_s is not UNSET:
            field_dict["timeout_s"] = timeout_s
        if retries is not UNSET:
            field_dict["retries"] = retries
        if start_period_s is not UNSET:
            field_dict["start_period_s"] = start_period_s

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        test = cast(list[str], d.pop("test", UNSET))

        def _parse_interval_s(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        interval_s = _parse_interval_s(d.pop("interval_s", UNSET))

        def _parse_timeout_s(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        timeout_s = _parse_timeout_s(d.pop("timeout_s", UNSET))

        def _parse_retries(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        retries = _parse_retries(d.pop("retries", UNSET))

        def _parse_start_period_s(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        start_period_s = _parse_start_period_s(d.pop("start_period_s", UNSET))

        app_manifest_healthcheck = cls(
            test=test,
            interval_s=interval_s,
            timeout_s=timeout_s,
            retries=retries,
            start_period_s=start_period_s,
        )

        app_manifest_healthcheck.additional_properties = d
        return app_manifest_healthcheck

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
