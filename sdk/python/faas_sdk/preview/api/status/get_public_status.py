from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.public_status_overview import PublicStatusOverview
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/status",
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> PublicStatusOverview | None:
    if response.status_code == 200:
        response_200 = PublicStatusOverview.from_dict(response.json())

        return response_200

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[PublicStatusOverview]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[PublicStatusOverview]:
    """Read the current public platform status.

     Unauthenticated single-region snapshot. Includes five public
    capabilities, exactly 30 UTC daily observations per capability,
    current error-budget indicators, active events, maintenance in the
    next 30 days, and up to 20 resolved incidents from the last 90 days.
    Data older than 90 seconds is marked stale; missing telemetry is never
    represented as operational.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PublicStatusOverview]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> PublicStatusOverview | None:
    """Read the current public platform status.

     Unauthenticated single-region snapshot. Includes five public
    capabilities, exactly 30 UTC daily observations per capability,
    current error-budget indicators, active events, maintenance in the
    next 30 days, and up to 20 resolved incidents from the last 90 days.
    Data older than 90 seconds is marked stale; missing telemetry is never
    represented as operational.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PublicStatusOverview
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[PublicStatusOverview]:
    """Read the current public platform status.

     Unauthenticated single-region snapshot. Includes five public
    capabilities, exactly 30 UTC daily observations per capability,
    current error-budget indicators, active events, maintenance in the
    next 30 days, and up to 20 resolved incidents from the last 90 days.
    Data older than 90 seconds is marked stale; missing telemetry is never
    represented as operational.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PublicStatusOverview]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> PublicStatusOverview | None:
    """Read the current public platform status.

     Unauthenticated single-region snapshot. Includes five public
    capabilities, exactly 30 UTC daily observations per capability,
    current error-budget indicators, active events, maintenance in the
    next 30 days, and up to 20 resolved incidents from the last 90 days.
    Data older than 90 seconds is marked stale; missing telemetry is never
    represented as operational.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PublicStatusOverview
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
