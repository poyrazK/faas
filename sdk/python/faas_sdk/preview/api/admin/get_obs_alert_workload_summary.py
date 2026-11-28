from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.obs_alert_workload_summary import ObsAlertWorkloadSummary
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/obs/incidents/platform_alert/summary",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ObsAlertWorkloadSummary | Problem | None:
    if response.status_code == 200:
        response_200 = ObsAlertWorkloadSummary.from_dict(response.json())

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
) -> Response[Any | ObsAlertWorkloadSummary | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | ObsAlertWorkloadSummary | Problem]:
    """Summarize retained platform alert workload and observation freshness.

     Requires operator read authorization, MFA and the operator allowlist.
    Counts all physically retained platform alert episodes in one database
    snapshot, including expired rows awaiting retention cleanup. There is
    no look-back window, pagination or query filter. Groups are exact
    intersections of status, severity, acknowledgment, ownership and snooze.
    Needs-attention includes unsnoozed unresolved episodes that are either
    unacknowledged or unowned, including unknown observations. Collection
    failure, stale rules and disabled collection cannot resolve an alert.
    Counts are null before the first successful collection. Age is elapsed
    time since episode activation, not time since assignment or acknowledgment.
    SQL failure returns unavailable rather than an empty workload.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsAlertWorkloadSummary | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> Any | ObsAlertWorkloadSummary | Problem | None:
    """Summarize retained platform alert workload and observation freshness.

     Requires operator read authorization, MFA and the operator allowlist.
    Counts all physically retained platform alert episodes in one database
    snapshot, including expired rows awaiting retention cleanup. There is
    no look-back window, pagination or query filter. Groups are exact
    intersections of status, severity, acknowledgment, ownership and snooze.
    Needs-attention includes unsnoozed unresolved episodes that are either
    unacknowledged or unowned, including unknown observations. Collection
    failure, stale rules and disabled collection cannot resolve an alert.
    Counts are null before the first successful collection. Age is elapsed
    time since episode activation, not time since assignment or acknowledgment.
    SQL failure returns unavailable rather than an empty workload.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsAlertWorkloadSummary | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | ObsAlertWorkloadSummary | Problem]:
    """Summarize retained platform alert workload and observation freshness.

     Requires operator read authorization, MFA and the operator allowlist.
    Counts all physically retained platform alert episodes in one database
    snapshot, including expired rows awaiting retention cleanup. There is
    no look-back window, pagination or query filter. Groups are exact
    intersections of status, severity, acknowledgment, ownership and snooze.
    Needs-attention includes unsnoozed unresolved episodes that are either
    unacknowledged or unowned, including unknown observations. Collection
    failure, stale rules and disabled collection cannot resolve an alert.
    Counts are null before the first successful collection. Age is elapsed
    time since episode activation, not time since assignment or acknowledgment.
    SQL failure returns unavailable rather than an empty workload.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsAlertWorkloadSummary | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> Any | ObsAlertWorkloadSummary | Problem | None:
    """Summarize retained platform alert workload and observation freshness.

     Requires operator read authorization, MFA and the operator allowlist.
    Counts all physically retained platform alert episodes in one database
    snapshot, including expired rows awaiting retention cleanup. There is
    no look-back window, pagination or query filter. Groups are exact
    intersections of status, severity, acknowledgment, ownership and snooze.
    Needs-attention includes unsnoozed unresolved episodes that are either
    unacknowledged or unowned, including unknown observations. Collection
    failure, stale rules and disabled collection cannot resolve an alert.
    Counts are null before the first successful collection. Age is elapsed
    time since episode activation, not time since assignment or acknowledgment.
    SQL failure returns unavailable rather than an empty workload.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsAlertWorkloadSummary | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
