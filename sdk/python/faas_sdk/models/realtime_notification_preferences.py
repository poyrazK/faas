from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.realtime_notification_preferences_digest_interval_seconds import (
    RealtimeNotificationPreferencesDigestIntervalSeconds,
    check_realtime_notification_preferences_digest_interval_seconds,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.realtime_notification_preferences_categories_type_0 import (
        RealtimeNotificationPreferencesCategoriesType0,
    )
    from ..models.realtime_notification_preferences_quiet_hours_type_0 import (
        RealtimeNotificationPreferencesQuietHoursType0,
    )
    from ..models.realtime_notification_preferences_rate_limit_type_0 import (
        RealtimeNotificationPreferencesRateLimitType0,
    )


T = TypeVar("T", bound="RealtimeNotificationPreferences")


@_attrs_define
class RealtimeNotificationPreferences:
    enabled: bool
    """Master push switch for this principal."""
    rate_limit: None | RealtimeNotificationPreferencesRateLimitType0 | Unset = UNSET
    """Shared per-principal provider-delivery quota across devices. Null or omission disables it. Digests consume
    one slot."""
    allow_urgent_bypass: bool | Unset = False
    """Allow urgent alerts to bypass quiet hours and digest delays; mute settings still apply."""
    digest_interval_seconds: RealtimeNotificationPreferencesDigestIntervalSeconds | Unset = 0
    """Immediate, five-minute or hourly UTC delivery windows."""
    summarize_quiet_hours: bool | None | Unset = True
    """Combine eligible alerts released after quiet hours; null inherits true."""
    categories: None | RealtimeNotificationPreferencesCategoriesType0 | Unset = UNSET
    """Unlisted categories are enabled. False cancels push for this category."""
    devices: list[str] | None | Unset = UNSET
    """Null or omitted selects all registered devices; empty array selects none."""
    quiet_hours: None | RealtimeNotificationPreferencesQuietHoursType0 | Unset = UNSET
    """Daily half-open quiet interval; start and end must differ. Omit or null to disable."""

    def to_dict(self) -> dict[str, Any]:
        from ..models.realtime_notification_preferences_categories_type_0 import (
            RealtimeNotificationPreferencesCategoriesType0,
        )
        from ..models.realtime_notification_preferences_quiet_hours_type_0 import (
            RealtimeNotificationPreferencesQuietHoursType0,
        )
        from ..models.realtime_notification_preferences_rate_limit_type_0 import (
            RealtimeNotificationPreferencesRateLimitType0,
        )

        enabled = self.enabled

        rate_limit: dict[str, Any] | None | Unset
        if isinstance(self.rate_limit, Unset):
            rate_limit = UNSET
        elif isinstance(self.rate_limit, RealtimeNotificationPreferencesRateLimitType0):
            rate_limit = self.rate_limit.to_dict()
        else:
            rate_limit = self.rate_limit

        allow_urgent_bypass = self.allow_urgent_bypass

        digest_interval_seconds: int | Unset = UNSET
        if not isinstance(self.digest_interval_seconds, Unset):
            digest_interval_seconds = self.digest_interval_seconds

        summarize_quiet_hours: bool | None | Unset
        if isinstance(self.summarize_quiet_hours, Unset):
            summarize_quiet_hours = UNSET
        else:
            summarize_quiet_hours = self.summarize_quiet_hours

        categories: dict[str, Any] | None | Unset
        if isinstance(self.categories, Unset):
            categories = UNSET
        elif isinstance(self.categories, RealtimeNotificationPreferencesCategoriesType0):
            categories = self.categories.to_dict()
        else:
            categories = self.categories

        devices: list[str] | None | Unset
        if isinstance(self.devices, Unset):
            devices = UNSET
        elif isinstance(self.devices, list):
            devices = self.devices

        else:
            devices = self.devices

        quiet_hours: dict[str, Any] | None | Unset
        if isinstance(self.quiet_hours, Unset):
            quiet_hours = UNSET
        elif isinstance(self.quiet_hours, RealtimeNotificationPreferencesQuietHoursType0):
            quiet_hours = self.quiet_hours.to_dict()
        else:
            quiet_hours = self.quiet_hours

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "enabled": enabled,
            }
        )
        if rate_limit is not UNSET:
            field_dict["rate_limit"] = rate_limit
        if allow_urgent_bypass is not UNSET:
            field_dict["allow_urgent_bypass"] = allow_urgent_bypass
        if digest_interval_seconds is not UNSET:
            field_dict["digest_interval_seconds"] = digest_interval_seconds
        if summarize_quiet_hours is not UNSET:
            field_dict["summarize_quiet_hours"] = summarize_quiet_hours
        if categories is not UNSET:
            field_dict["categories"] = categories
        if devices is not UNSET:
            field_dict["devices"] = devices
        if quiet_hours is not UNSET:
            field_dict["quiet_hours"] = quiet_hours

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.realtime_notification_preferences_categories_type_0 import (
            RealtimeNotificationPreferencesCategoriesType0,
        )
        from ..models.realtime_notification_preferences_quiet_hours_type_0 import (
            RealtimeNotificationPreferencesQuietHoursType0,
        )
        from ..models.realtime_notification_preferences_rate_limit_type_0 import (
            RealtimeNotificationPreferencesRateLimitType0,
        )

        d = dict(src_dict)
        enabled = d.pop("enabled")

        def _parse_rate_limit(data: object) -> None | RealtimeNotificationPreferencesRateLimitType0 | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                rate_limit_type_0 = RealtimeNotificationPreferencesRateLimitType0.from_dict(data)

                return rate_limit_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | RealtimeNotificationPreferencesRateLimitType0 | Unset, data)

        rate_limit = _parse_rate_limit(d.pop("rate_limit", UNSET))

        allow_urgent_bypass = d.pop("allow_urgent_bypass", UNSET)

        _digest_interval_seconds = d.pop("digest_interval_seconds", UNSET)
        digest_interval_seconds: RealtimeNotificationPreferencesDigestIntervalSeconds | Unset
        if isinstance(_digest_interval_seconds, Unset):
            digest_interval_seconds = UNSET
        else:
            digest_interval_seconds = check_realtime_notification_preferences_digest_interval_seconds(
                _digest_interval_seconds
            )

        def _parse_summarize_quiet_hours(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        summarize_quiet_hours = _parse_summarize_quiet_hours(d.pop("summarize_quiet_hours", UNSET))

        def _parse_categories(data: object) -> None | RealtimeNotificationPreferencesCategoriesType0 | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                categories_type_0 = RealtimeNotificationPreferencesCategoriesType0.from_dict(data)

                return categories_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | RealtimeNotificationPreferencesCategoriesType0 | Unset, data)

        categories = _parse_categories(d.pop("categories", UNSET))

        def _parse_devices(data: object) -> list[str] | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, list):
                    raise TypeError()
                devices_type_0 = cast(list[str], data)

                return devices_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(list[str] | None | Unset, data)

        devices = _parse_devices(d.pop("devices", UNSET))

        def _parse_quiet_hours(data: object) -> None | RealtimeNotificationPreferencesQuietHoursType0 | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                quiet_hours_type_0 = RealtimeNotificationPreferencesQuietHoursType0.from_dict(data)

                return quiet_hours_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | RealtimeNotificationPreferencesQuietHoursType0 | Unset, data)

        quiet_hours = _parse_quiet_hours(d.pop("quiet_hours", UNSET))

        realtime_notification_preferences = cls(
            enabled=enabled,
            rate_limit=rate_limit,
            allow_urgent_bypass=allow_urgent_bypass,
            digest_interval_seconds=digest_interval_seconds,
            summarize_quiet_hours=summarize_quiet_hours,
            categories=categories,
            devices=devices,
            quiet_hours=quiet_hours,
        )

        return realtime_notification_preferences
