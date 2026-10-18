import datetime
from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.obs_alert_response_times import ObsAlertResponseTimes
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
        "url": "/v1/admin/obs/incidents/platform_alert/response-times",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ObsAlertResponseTimes | Problem | None:
    if response.status_code == 200:
        response_200 = ObsAlertResponseTimes.from_dict(response.json())

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
) -> Response[Any | ObsAlertResponseTimes | Problem]:
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
) -> Response[Any | ObsAlertResponseTimes | Problem]:
    """Measure retained alert acknowledgment and observed resolution timing.

     Reads retained platform alert episodes activated between since and as_of,
    inclusive. The default window is 24 hours with a maximum of 30 days.
    Receipt coverage must prove the complete revision sequence before first
    acknowledgment is included. Invalid timestamps are excluded from samples.
    Unknown and unobserved outcomes remain in explicit cohort denominators.
    Resolution measures confirmed rule recovery rather than actual repair.
    Discrete nearest-rank percentiles use whole-second valid samples only.
    Cohorts above 1000 episodes or 10000 matching receipts return 400 and
    require a narrower since; results are never silently sampled. Reads
    share one database snapshot and a five-second query budget. Retention
    and collection metadata do not guarantee a complete historical window.
    Prospective cadence coverage is read in the same snapshot. Its separate
    100000-outcome read bound also returns 400 requiring a narrower since.

    Args:
        since (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsAlertResponseTimes | Problem]
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
) -> Any | ObsAlertResponseTimes | Problem | None:
    """Measure retained alert acknowledgment and observed resolution timing.

     Reads retained platform alert episodes activated between since and as_of,
    inclusive. The default window is 24 hours with a maximum of 30 days.
    Receipt coverage must prove the complete revision sequence before first
    acknowledgment is included. Invalid timestamps are excluded from samples.
    Unknown and unobserved outcomes remain in explicit cohort denominators.
    Resolution measures confirmed rule recovery rather than actual repair.
    Discrete nearest-rank percentiles use whole-second valid samples only.
    Cohorts above 1000 episodes or 10000 matching receipts return 400 and
    require a narrower since; results are never silently sampled. Reads
    share one database snapshot and a five-second query budget. Retention
    and collection metadata do not guarantee a complete historical window.
    Prospective cadence coverage is read in the same snapshot. Its separate
    100000-outcome read bound also returns 400 requiring a narrower since.

    Args:
        since (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsAlertResponseTimes | Problem
    """

    return sync_detailed(
        client=client,
        since=since,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime | Unset = UNSET,
) -> Response[Any | ObsAlertResponseTimes | Problem]:
    """Measure retained alert acknowledgment and observed resolution timing.

     Reads retained platform alert episodes activated between since and as_of,
    inclusive. The default window is 24 hours with a maximum of 30 days.
    Receipt coverage must prove the complete revision sequence before first
    acknowledgment is included. Invalid timestamps are excluded from samples.
    Unknown and unobserved outcomes remain in explicit cohort denominators.
    Resolution measures confirmed rule recovery rather than actual repair.
    Discrete nearest-rank percentiles use whole-second valid samples only.
    Cohorts above 1000 episodes or 10000 matching receipts return 400 and
    require a narrower since; results are never silently sampled. Reads
    share one database snapshot and a five-second query budget. Retention
    and collection metadata do not guarantee a complete historical window.
    Prospective cadence coverage is read in the same snapshot. Its separate
    100000-outcome read bound also returns 400 requiring a narrower since.

    Args:
        since (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsAlertResponseTimes | Problem]
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
) -> Any | ObsAlertResponseTimes | Problem | None:
    """Measure retained alert acknowledgment and observed resolution timing.

     Reads retained platform alert episodes activated between since and as_of,
    inclusive. The default window is 24 hours with a maximum of 30 days.
    Receipt coverage must prove the complete revision sequence before first
    acknowledgment is included. Invalid timestamps are excluded from samples.
    Unknown and unobserved outcomes remain in explicit cohort denominators.
    Resolution measures confirmed rule recovery rather than actual repair.
    Discrete nearest-rank percentiles use whole-second valid samples only.
    Cohorts above 1000 episodes or 10000 matching receipts return 400 and
    require a narrower since; results are never silently sampled. Reads
    share one database snapshot and a five-second query budget. Retention
    and collection metadata do not guarantee a complete historical window.
    Prospective cadence coverage is read in the same snapshot. Its separate
    100000-outcome read bound also returns 400 requiring a narrower since.

    Args:
        since (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsAlertResponseTimes | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            since=since,
        )
    ).parsed
