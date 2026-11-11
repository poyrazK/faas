from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.data_api_status import DataAPIStatus
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/data-api/status".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> DataAPIStatus | Problem:
    if response.status_code == 200:
        response_200 = DataAPIStatus.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DataAPIStatus | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
) -> Response[DataAPIStatus | Problem]:
    """Read a Data API's platform status

     Returns what the platform knows about the app's Data API without
    waking it: the runtime version this control plane serves, whether
    this installation accepts runtime reports, and the change hook
    delivery the app's runtime last reported. Without runtime reports,
    usage reports, monthly caps, keyless live reads and change hooks do
    not reach the platform. `change_hooks` is absent until a runtime with
    change hooks on has reported.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataAPIStatus | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
) -> DataAPIStatus | Problem | None:
    """Read a Data API's platform status

     Returns what the platform knows about the app's Data API without
    waking it: the runtime version this control plane serves, whether
    this installation accepts runtime reports, and the change hook
    delivery the app's runtime last reported. Without runtime reports,
    usage reports, monthly caps, keyless live reads and change hooks do
    not reach the platform. `change_hooks` is absent until a runtime with
    change hooks on has reported.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataAPIStatus | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
) -> Response[DataAPIStatus | Problem]:
    """Read a Data API's platform status

     Returns what the platform knows about the app's Data API without
    waking it: the runtime version this control plane serves, whether
    this installation accepts runtime reports, and the change hook
    delivery the app's runtime last reported. Without runtime reports,
    usage reports, monthly caps, keyless live reads and change hooks do
    not reach the platform. `change_hooks` is absent until a runtime with
    change hooks on has reported.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataAPIStatus | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
) -> DataAPIStatus | Problem | None:
    """Read a Data API's platform status

     Returns what the platform knows about the app's Data API without
    waking it: the runtime version this control plane serves, whether
    this installation accepts runtime reports, and the change hook
    delivery the app's runtime last reported. Without runtime reports,
    usage reports, monthly caps, keyless live reads and change hooks do
    not reach the platform. `change_hooks` is absent until a runtime with
    change hooks on has reported.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataAPIStatus | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
        )
    ).parsed
