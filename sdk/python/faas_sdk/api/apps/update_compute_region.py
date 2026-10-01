from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.compute_region_response import ComputeRegionResponse
from ...models.problem import Problem
from ...models.update_compute_region_request import UpdateComputeRegionRequest
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: UpdateComputeRegionRequest,
    idempotency_key: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/regions/{id}".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ComputeRegionResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ComputeRegionResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ComputeRegionResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateComputeRegionRequest,
    idempotency_key: str,
) -> Response[ComputeRegionResponse | Problem]:
    """Declare compute region policy.

     Operator-only complete policy update. Requires an allowlisted operator session, fresh MFA step-up,
    same-origin protection, and an Idempotency-Key. Pausing admission blocks new apps, previews,
    projects, and managed job definitions; existing workloads retain their region and may execute or
    recover within it. Does not evict instances.

    Args:
        id (str):
        idempotency_key (str):
        body (UpdateComputeRegionRequest): Complete operator-controlled compute region policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ComputeRegionResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateComputeRegionRequest,
    idempotency_key: str,
) -> ComputeRegionResponse | Problem | None:
    """Declare compute region policy.

     Operator-only complete policy update. Requires an allowlisted operator session, fresh MFA step-up,
    same-origin protection, and an Idempotency-Key. Pausing admission blocks new apps, previews,
    projects, and managed job definitions; existing workloads retain their region and may execute or
    recover within it. Does not evict instances.

    Args:
        id (str):
        idempotency_key (str):
        body (UpdateComputeRegionRequest): Complete operator-controlled compute region policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ComputeRegionResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateComputeRegionRequest,
    idempotency_key: str,
) -> Response[ComputeRegionResponse | Problem]:
    """Declare compute region policy.

     Operator-only complete policy update. Requires an allowlisted operator session, fresh MFA step-up,
    same-origin protection, and an Idempotency-Key. Pausing admission blocks new apps, previews,
    projects, and managed job definitions; existing workloads retain their region and may execute or
    recover within it. Does not evict instances.

    Args:
        id (str):
        idempotency_key (str):
        body (UpdateComputeRegionRequest): Complete operator-controlled compute region policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ComputeRegionResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateComputeRegionRequest,
    idempotency_key: str,
) -> ComputeRegionResponse | Problem | None:
    """Declare compute region policy.

     Operator-only complete policy update. Requires an allowlisted operator session, fresh MFA step-up,
    same-origin protection, and an Idempotency-Key. Pausing admission blocks new apps, previews,
    projects, and managed job definitions; existing workloads retain their region and may execute or
    recover within it. Does not evict instances.

    Args:
        id (str):
        idempotency_key (str):
        body (UpdateComputeRegionRequest): Complete operator-controlled compute region policy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ComputeRegionResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
