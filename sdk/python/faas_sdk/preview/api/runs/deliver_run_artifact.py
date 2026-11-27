from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.deliver_run_artifact_request import DeliverRunArtifactRequest
from ...models.problem import Problem
from ...models.run_artifact_delivery import RunArtifactDelivery
from ...types import Response


def _get_kwargs(
    slug: str,
    route: str,
    *,
    body: DeliverRunArtifactRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/run-artifact-deliveries/{route}".format(
            slug=quote(str(slug), safe=""),
            route=quote(str(route), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | Problem | RunArtifactDelivery | None:
    if response.status_code == 200:
        response_200 = RunArtifactDelivery.from_dict(response.json())

        return response_200

    if response.status_code == 202:
        response_202 = RunArtifactDelivery.from_dict(response.json())

        return response_202

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = cast(Any, None)
        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

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
) -> Response[Any | Problem | RunArtifactDelivery]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    route: str,
    *,
    client: AuthenticatedClient,
    body: DeliverRunArtifactRequest,
) -> Response[Any | Problem | RunArtifactDelivery]:
    """Deliver a verified Run artifact into an app-owned upload route

     Requires account deploy authority and a bucket write grant, or a live production workload identity
    for the destination app. New delivery checks the source Run or delegated Agent receipt. The explicit
    delivery key deduplicates storage writes independently of execution. Replay survives source expiry,
    thread revocation and disabled Run writers. Pending outcomes are resolved by the existing object
    upload recovery worker without reuploading or rerunning code.

    Args:
        slug (str):
        route (str):
        body (DeliverRunArtifactRequest): Exact Run output and stable storage intent to promote
            into an application upload route.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | RunArtifactDelivery]
    """

    kwargs = _get_kwargs(
        slug=slug,
        route=route,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    route: str,
    *,
    client: AuthenticatedClient,
    body: DeliverRunArtifactRequest,
) -> Any | Problem | RunArtifactDelivery | None:
    """Deliver a verified Run artifact into an app-owned upload route

     Requires account deploy authority and a bucket write grant, or a live production workload identity
    for the destination app. New delivery checks the source Run or delegated Agent receipt. The explicit
    delivery key deduplicates storage writes independently of execution. Replay survives source expiry,
    thread revocation and disabled Run writers. Pending outcomes are resolved by the existing object
    upload recovery worker without reuploading or rerunning code.

    Args:
        slug (str):
        route (str):
        body (DeliverRunArtifactRequest): Exact Run output and stable storage intent to promote
            into an application upload route.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | RunArtifactDelivery
    """

    return sync_detailed(
        slug=slug,
        route=route,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    route: str,
    *,
    client: AuthenticatedClient,
    body: DeliverRunArtifactRequest,
) -> Response[Any | Problem | RunArtifactDelivery]:
    """Deliver a verified Run artifact into an app-owned upload route

     Requires account deploy authority and a bucket write grant, or a live production workload identity
    for the destination app. New delivery checks the source Run or delegated Agent receipt. The explicit
    delivery key deduplicates storage writes independently of execution. Replay survives source expiry,
    thread revocation and disabled Run writers. Pending outcomes are resolved by the existing object
    upload recovery worker without reuploading or rerunning code.

    Args:
        slug (str):
        route (str):
        body (DeliverRunArtifactRequest): Exact Run output and stable storage intent to promote
            into an application upload route.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | RunArtifactDelivery]
    """

    kwargs = _get_kwargs(
        slug=slug,
        route=route,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    route: str,
    *,
    client: AuthenticatedClient,
    body: DeliverRunArtifactRequest,
) -> Any | Problem | RunArtifactDelivery | None:
    """Deliver a verified Run artifact into an app-owned upload route

     Requires account deploy authority and a bucket write grant, or a live production workload identity
    for the destination app. New delivery checks the source Run or delegated Agent receipt. The explicit
    delivery key deduplicates storage writes independently of execution. Replay survives source expiry,
    thread revocation and disabled Run writers. Pending outcomes are resolved by the existing object
    upload recovery worker without reuploading or rerunning code.

    Args:
        slug (str):
        route (str):
        body (DeliverRunArtifactRequest): Exact Run output and stable storage intent to promote
            into an application upload route.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | RunArtifactDelivery
    """

    return (
        await asyncio_detailed(
            slug=slug,
            route=route,
            client=client,
            body=body,
        )
    ).parsed
