import datetime
from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.obs_alert_collection_history import ObsAlertCollectionHistory
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    since: datetime.datetime | Unset = UNSET,
    details: bool | Unset = False,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_since: str | Unset = UNSET
    if not isinstance(since, Unset):
        json_since = since.isoformat()
    params["since"] = json_since

    params["details"] = details

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/obs/collection-history",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ObsAlertCollectionHistory | Problem | None:
    if response.status_code == 200:
        response_200 = ObsAlertCollectionHistory.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = cast(Any, None)
        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ObsAlertCollectionHistory | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    details: bool | Unset = False,
) -> Response[Any | ObsAlertCollectionHistory | Problem]:
    """Inspect prospective collection outcomes and missing-observation intervals.

     Requires operator read scope, MFA and the operator allowlist. Accepts one
    optional nonempty since timestamp, default 24 hours and maximum 30 days,
    and an optional details=true or details=false flag.
    Reads prospective immutable collection outcomes from one read-only
    snapshot with a five-second budget. Success commits atomically with
    retained episode changes; failures contain closed sanitized reason codes.
    Each outcome supplies at most 60 seconds of collection cadence evidence,
    ending sooner at the next outcome. Missing intervals, periods before
    recording began, expired evidence and unrecordable outages remain unknown.
    These intervals are not continuous availability or proof of expected
    rule inventory, metric coverage or complete incident history.
    Microsecond duration partitions cover the full half-open [since, as_of)
    window exactly. Counts include outcomes at since and as_of; a retained
    carry-in outcome can contribute duration without increasing those counts.
    Outcomes before the 30-day retention horizon or after as_of are excluded.
    More than 100000 read outcomes returns 400 requiring a narrower since.
    Totals precede a newest-first preview of at most 200 coalesced intervals
    with explicit truncation. Current disabled configuration does not erase
    historical evidence. With details=true, also returns up to 20 newest failed
    attempts inside [since, as_of] from the same snapshot, separately from
    coalesced intervals. Every failure counts toward the complete total,
    including those without diagnostics. Recorded rejection diagnostics
    describe the exact bounded source snapshot at attempted_at, never a
    later live rule-health read. Complete rule/group counts precede up to
    10 unhealthy rule/group items and 16 KiB of compact diagnostic JSON.
    Missing older diagnostics and unreadable sources remain unknown.
    Observed issues do not prove the underlying root cause or which issue
    triggered the collector's first rejection. No raw query, evaluation
    error, label, absolute path or arbitrary runbook URL is retained.
    No lifecycle or triage writes occur on GET.

    Args:
        since (datetime.datetime | Unset):
        details (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsAlertCollectionHistory | Problem]
    """

    kwargs = _get_kwargs(
        since=since,
        details=details,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    details: bool | Unset = False,
) -> Any | ObsAlertCollectionHistory | Problem | None:
    """Inspect prospective collection outcomes and missing-observation intervals.

     Requires operator read scope, MFA and the operator allowlist. Accepts one
    optional nonempty since timestamp, default 24 hours and maximum 30 days,
    and an optional details=true or details=false flag.
    Reads prospective immutable collection outcomes from one read-only
    snapshot with a five-second budget. Success commits atomically with
    retained episode changes; failures contain closed sanitized reason codes.
    Each outcome supplies at most 60 seconds of collection cadence evidence,
    ending sooner at the next outcome. Missing intervals, periods before
    recording began, expired evidence and unrecordable outages remain unknown.
    These intervals are not continuous availability or proof of expected
    rule inventory, metric coverage or complete incident history.
    Microsecond duration partitions cover the full half-open [since, as_of)
    window exactly. Counts include outcomes at since and as_of; a retained
    carry-in outcome can contribute duration without increasing those counts.
    Outcomes before the 30-day retention horizon or after as_of are excluded.
    More than 100000 read outcomes returns 400 requiring a narrower since.
    Totals precede a newest-first preview of at most 200 coalesced intervals
    with explicit truncation. Current disabled configuration does not erase
    historical evidence. With details=true, also returns up to 20 newest failed
    attempts inside [since, as_of] from the same snapshot, separately from
    coalesced intervals. Every failure counts toward the complete total,
    including those without diagnostics. Recorded rejection diagnostics
    describe the exact bounded source snapshot at attempted_at, never a
    later live rule-health read. Complete rule/group counts precede up to
    10 unhealthy rule/group items and 16 KiB of compact diagnostic JSON.
    Missing older diagnostics and unreadable sources remain unknown.
    Observed issues do not prove the underlying root cause or which issue
    triggered the collector's first rejection. No raw query, evaluation
    error, label, absolute path or arbitrary runbook URL is retained.
    No lifecycle or triage writes occur on GET.

    Args:
        since (datetime.datetime | Unset):
        details (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsAlertCollectionHistory | Problem
    """

    return sync_detailed(
        client=client,
        since=since,
        details=details,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    details: bool | Unset = False,
) -> Response[Any | ObsAlertCollectionHistory | Problem]:
    """Inspect prospective collection outcomes and missing-observation intervals.

     Requires operator read scope, MFA and the operator allowlist. Accepts one
    optional nonempty since timestamp, default 24 hours and maximum 30 days,
    and an optional details=true or details=false flag.
    Reads prospective immutable collection outcomes from one read-only
    snapshot with a five-second budget. Success commits atomically with
    retained episode changes; failures contain closed sanitized reason codes.
    Each outcome supplies at most 60 seconds of collection cadence evidence,
    ending sooner at the next outcome. Missing intervals, periods before
    recording began, expired evidence and unrecordable outages remain unknown.
    These intervals are not continuous availability or proof of expected
    rule inventory, metric coverage or complete incident history.
    Microsecond duration partitions cover the full half-open [since, as_of)
    window exactly. Counts include outcomes at since and as_of; a retained
    carry-in outcome can contribute duration without increasing those counts.
    Outcomes before the 30-day retention horizon or after as_of are excluded.
    More than 100000 read outcomes returns 400 requiring a narrower since.
    Totals precede a newest-first preview of at most 200 coalesced intervals
    with explicit truncation. Current disabled configuration does not erase
    historical evidence. With details=true, also returns up to 20 newest failed
    attempts inside [since, as_of] from the same snapshot, separately from
    coalesced intervals. Every failure counts toward the complete total,
    including those without diagnostics. Recorded rejection diagnostics
    describe the exact bounded source snapshot at attempted_at, never a
    later live rule-health read. Complete rule/group counts precede up to
    10 unhealthy rule/group items and 16 KiB of compact diagnostic JSON.
    Missing older diagnostics and unreadable sources remain unknown.
    Observed issues do not prove the underlying root cause or which issue
    triggered the collector's first rejection. No raw query, evaluation
    error, label, absolute path or arbitrary runbook URL is retained.
    No lifecycle or triage writes occur on GET.

    Args:
        since (datetime.datetime | Unset):
        details (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsAlertCollectionHistory | Problem]
    """

    kwargs = _get_kwargs(
        since=since,
        details=details,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
    details: bool | Unset = False,
) -> Any | ObsAlertCollectionHistory | Problem | None:
    """Inspect prospective collection outcomes and missing-observation intervals.

     Requires operator read scope, MFA and the operator allowlist. Accepts one
    optional nonempty since timestamp, default 24 hours and maximum 30 days,
    and an optional details=true or details=false flag.
    Reads prospective immutable collection outcomes from one read-only
    snapshot with a five-second budget. Success commits atomically with
    retained episode changes; failures contain closed sanitized reason codes.
    Each outcome supplies at most 60 seconds of collection cadence evidence,
    ending sooner at the next outcome. Missing intervals, periods before
    recording began, expired evidence and unrecordable outages remain unknown.
    These intervals are not continuous availability or proof of expected
    rule inventory, metric coverage or complete incident history.
    Microsecond duration partitions cover the full half-open [since, as_of)
    window exactly. Counts include outcomes at since and as_of; a retained
    carry-in outcome can contribute duration without increasing those counts.
    Outcomes before the 30-day retention horizon or after as_of are excluded.
    More than 100000 read outcomes returns 400 requiring a narrower since.
    Totals precede a newest-first preview of at most 200 coalesced intervals
    with explicit truncation. Current disabled configuration does not erase
    historical evidence. With details=true, also returns up to 20 newest failed
    attempts inside [since, as_of] from the same snapshot, separately from
    coalesced intervals. Every failure counts toward the complete total,
    including those without diagnostics. Recorded rejection diagnostics
    describe the exact bounded source snapshot at attempted_at, never a
    later live rule-health read. Complete rule/group counts precede up to
    10 unhealthy rule/group items and 16 KiB of compact diagnostic JSON.
    Missing older diagnostics and unreadable sources remain unknown.
    Observed issues do not prove the underlying root cause or which issue
    triggered the collector's first rejection. No raw query, evaluation
    error, label, absolute path or arbitrary runbook URL is retained.
    No lifecycle or triage writes occur on GET.

    Args:
        since (datetime.datetime | Unset):
        details (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsAlertCollectionHistory | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            since=since,
            details=details,
        )
    ).parsed
