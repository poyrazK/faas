from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.resource_status import ResourceStatus
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    environment: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/status".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Problem | ResourceStatus:
    if response.status_code == 200:
        response_200 = ResourceStatus.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ResourceStatus]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    environment: str | Unset = UNSET,
) -> Response[Problem | ResourceStatus]:
    """Read an app's conditions.

     Preview (resource-conditions capability). The app's Ready,
    Progressing and Degraded conditions, derived when read
    from the same evidence as GET /v1/apps/{slug}/health and never
    stored. It never wakes or probes a workload. An idle app that scales
    to zero is Ready=True with reason idle. A terminal condition will not
    change without a new request. Requires apps:read or admin scope. No
    MFA required. Cache-Control no-store.

    Args:
        slug (str):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ResourceStatus]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    environment: str | Unset = UNSET,
) -> Problem | ResourceStatus | None:
    """Read an app's conditions.

     Preview (resource-conditions capability). The app's Ready,
    Progressing and Degraded conditions, derived when read
    from the same evidence as GET /v1/apps/{slug}/health and never
    stored. It never wakes or probes a workload. An idle app that scales
    to zero is Ready=True with reason idle. A terminal condition will not
    change without a new request. Requires apps:read or admin scope. No
    MFA required. Cache-Control no-store.

    Args:
        slug (str):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ResourceStatus
    """

    return sync_detailed(
        slug=slug,
        client=client,
        environment=environment,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    environment: str | Unset = UNSET,
) -> Response[Problem | ResourceStatus]:
    """Read an app's conditions.

     Preview (resource-conditions capability). The app's Ready,
    Progressing and Degraded conditions, derived when read
    from the same evidence as GET /v1/apps/{slug}/health and never
    stored. It never wakes or probes a workload. An idle app that scales
    to zero is Ready=True with reason idle. A terminal condition will not
    change without a new request. Requires apps:read or admin scope. No
    MFA required. Cache-Control no-store.

    Args:
        slug (str):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ResourceStatus]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    environment: str | Unset = UNSET,
) -> Problem | ResourceStatus | None:
    """Read an app's conditions.

     Preview (resource-conditions capability). The app's Ready,
    Progressing and Degraded conditions, derived when read
    from the same evidence as GET /v1/apps/{slug}/health and never
    stored. It never wakes or probes a workload. An idle app that scales
    to zero is Ready=True with reason idle. A terminal condition will not
    change without a new request. Requires apps:read or admin scope. No
    MFA required. Cache-Control no-store.

    Args:
        slug (str):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ResourceStatus
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            environment=environment,
        )
    ).parsed
