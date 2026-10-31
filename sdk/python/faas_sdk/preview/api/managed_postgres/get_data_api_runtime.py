from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.data_api_runtime import DataAPIRuntime
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/data-api/runtime".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> DataAPIRuntime | Problem:
    if response.status_code == 200:
        response_200 = DataAPIRuntime.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DataAPIRuntime | Problem]:
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
) -> Response[DataAPIRuntime | Problem]:
    """Download the current Data API runtime

     Returns the Data API runtime this control plane serves: its version,
    and a gzipped tar of the runtime under one `data-api/` directory with
    its SHA-256. `gregale data-api create` and `gregale data-api deploy`
    deploy this archive, so an older CLI release does not redeploy an older
    runtime. A running Data API reports the version it was built from as
    `runtime_version` on `/healthz`; a different version is not the
    platform's current runtime (ADR-20261031095954).

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataAPIRuntime | Problem]
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
) -> DataAPIRuntime | Problem | None:
    """Download the current Data API runtime

     Returns the Data API runtime this control plane serves: its version,
    and a gzipped tar of the runtime under one `data-api/` directory with
    its SHA-256. `gregale data-api create` and `gregale data-api deploy`
    deploy this archive, so an older CLI release does not redeploy an older
    runtime. A running Data API reports the version it was built from as
    `runtime_version` on `/healthz`; a different version is not the
    platform's current runtime (ADR-20261031095954).

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataAPIRuntime | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
) -> Response[DataAPIRuntime | Problem]:
    """Download the current Data API runtime

     Returns the Data API runtime this control plane serves: its version,
    and a gzipped tar of the runtime under one `data-api/` directory with
    its SHA-256. `gregale data-api create` and `gregale data-api deploy`
    deploy this archive, so an older CLI release does not redeploy an older
    runtime. A running Data API reports the version it was built from as
    `runtime_version` on `/healthz`; a different version is not the
    platform's current runtime (ADR-20261031095954).

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataAPIRuntime | Problem]
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
) -> DataAPIRuntime | Problem | None:
    """Download the current Data API runtime

     Returns the Data API runtime this control plane serves: its version,
    and a gzipped tar of the runtime under one `data-api/` directory with
    its SHA-256. `gregale data-api create` and `gregale data-api deploy`
    deploy this archive, so an older CLI release does not redeploy an older
    runtime. A running Data API reports the version it was built from as
    `runtime_version` on `/healthz`; a different version is not the
    platform's current runtime (ADR-20261031095954).

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataAPIRuntime | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
        )
    ).parsed
