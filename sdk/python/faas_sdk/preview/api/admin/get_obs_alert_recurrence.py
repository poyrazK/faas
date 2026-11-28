import datetime
from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.obs_alert_recurrence import ObsAlertRecurrence
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    since: datetime.datetime | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_since: str | Unset = UNSET
    if not isinstance(since, Unset):
        json_since = since.isoformat()
    params["since"] = json_since

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/obs/incidents/platform_alert/recurrence",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ObsAlertRecurrence | Problem | None:
    if response.status_code == 200:
        response_200 = ObsAlertRecurrence.from_dict(response.json())

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
) -> Response[Any | ObsAlertRecurrence | Problem]:
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
) -> Response[Any | ObsAlertRecurrence | Problem]:
    """Diagnose repeated retained alert activations and observed recovery gaps.

     Requires operator read scope, MFA and the operator allowlist. Reads one
    retained activation cohort between since and as_of inclusive, default
    24 hours and maximum 30 days. Groups by rule definition and exact series;
    changed rules and different resources never share a recurrence sequence.
    Repeat activations equal episodes minus one within each group. Durations
    measure activation to confirmed resolution observation. Recovery gaps
    compare adjacent activations only when the previous confirmed resolution
    precedes or equals the next activation; unknown recovery, overlapping
    observations and invalid clocks are counted separately. Minimum and
    nearest-rank median use valid whole-second samples; absent samples are
    null. This does not establish exact recovery time, continuous collection,
    lifetime recurrence, false positives or notification volume.
    Cohorts above 1000 episodes return 400 requiring a narrower since.
    Complete totals precede a preview of at most 200 series ranked by repeat
    count descending, latest activation descending, rule key then series ID.
    Each preview includes at most five recent episodes newest first with
    explicit truncation. One read-only database snapshot has a five-second
    budget. Current collection and retention metadata do not reconstruct
    expected inventory or per-rule coverage gaps. Prospective cadence evidence
    accompanies this report; its own 100000-outcome bound also requires a
    narrower since on overflow. Reads perform no lifecycle or triage writes.

    Args:
        since (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsAlertRecurrence | Problem]
    """

    kwargs = _get_kwargs(
        since=since,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
) -> Any | ObsAlertRecurrence | Problem | None:
    """Diagnose repeated retained alert activations and observed recovery gaps.

     Requires operator read scope, MFA and the operator allowlist. Reads one
    retained activation cohort between since and as_of inclusive, default
    24 hours and maximum 30 days. Groups by rule definition and exact series;
    changed rules and different resources never share a recurrence sequence.
    Repeat activations equal episodes minus one within each group. Durations
    measure activation to confirmed resolution observation. Recovery gaps
    compare adjacent activations only when the previous confirmed resolution
    precedes or equals the next activation; unknown recovery, overlapping
    observations and invalid clocks are counted separately. Minimum and
    nearest-rank median use valid whole-second samples; absent samples are
    null. This does not establish exact recovery time, continuous collection,
    lifetime recurrence, false positives or notification volume.
    Cohorts above 1000 episodes return 400 requiring a narrower since.
    Complete totals precede a preview of at most 200 series ranked by repeat
    count descending, latest activation descending, rule key then series ID.
    Each preview includes at most five recent episodes newest first with
    explicit truncation. One read-only database snapshot has a five-second
    budget. Current collection and retention metadata do not reconstruct
    expected inventory or per-rule coverage gaps. Prospective cadence evidence
    accompanies this report; its own 100000-outcome bound also requires a
    narrower since on overflow. Reads perform no lifecycle or triage writes.

    Args:
        since (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsAlertRecurrence | Problem
    """

    return sync_detailed(
        client=client,
        since=since,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
) -> Response[Any | ObsAlertRecurrence | Problem]:
    """Diagnose repeated retained alert activations and observed recovery gaps.

     Requires operator read scope, MFA and the operator allowlist. Reads one
    retained activation cohort between since and as_of inclusive, default
    24 hours and maximum 30 days. Groups by rule definition and exact series;
    changed rules and different resources never share a recurrence sequence.
    Repeat activations equal episodes minus one within each group. Durations
    measure activation to confirmed resolution observation. Recovery gaps
    compare adjacent activations only when the previous confirmed resolution
    precedes or equals the next activation; unknown recovery, overlapping
    observations and invalid clocks are counted separately. Minimum and
    nearest-rank median use valid whole-second samples; absent samples are
    null. This does not establish exact recovery time, continuous collection,
    lifetime recurrence, false positives or notification volume.
    Cohorts above 1000 episodes return 400 requiring a narrower since.
    Complete totals precede a preview of at most 200 series ranked by repeat
    count descending, latest activation descending, rule key then series ID.
    Each preview includes at most five recent episodes newest first with
    explicit truncation. One read-only database snapshot has a five-second
    budget. Current collection and retention metadata do not reconstruct
    expected inventory or per-rule coverage gaps. Prospective cadence evidence
    accompanies this report; its own 100000-outcome bound also requires a
    narrower since on overflow. Reads perform no lifecycle or triage writes.

    Args:
        since (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsAlertRecurrence | Problem]
    """

    kwargs = _get_kwargs(
        since=since,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
) -> Any | ObsAlertRecurrence | Problem | None:
    """Diagnose repeated retained alert activations and observed recovery gaps.

     Requires operator read scope, MFA and the operator allowlist. Reads one
    retained activation cohort between since and as_of inclusive, default
    24 hours and maximum 30 days. Groups by rule definition and exact series;
    changed rules and different resources never share a recurrence sequence.
    Repeat activations equal episodes minus one within each group. Durations
    measure activation to confirmed resolution observation. Recovery gaps
    compare adjacent activations only when the previous confirmed resolution
    precedes or equals the next activation; unknown recovery, overlapping
    observations and invalid clocks are counted separately. Minimum and
    nearest-rank median use valid whole-second samples; absent samples are
    null. This does not establish exact recovery time, continuous collection,
    lifetime recurrence, false positives or notification volume.
    Cohorts above 1000 episodes return 400 requiring a narrower since.
    Complete totals precede a preview of at most 200 series ranked by repeat
    count descending, latest activation descending, rule key then series ID.
    Each preview includes at most five recent episodes newest first with
    explicit truncation. One read-only database snapshot has a five-second
    budget. Current collection and retention metadata do not reconstruct
    expected inventory or per-rule coverage gaps. Prospective cadence evidence
    accompanies this report; its own 100000-outcome bound also requires a
    narrower since on overflow. Reads perform no lifecycle or triage writes.

    Args:
        since (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsAlertRecurrence | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            since=since,
        )
    ).parsed
