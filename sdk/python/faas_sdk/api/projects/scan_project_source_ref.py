from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.plan_response import PlanResponse
from ...models.problem import Problem
from ...models.project_source_ref_scan_request import ProjectSourceRefScanRequest
from ...types import Response


def _get_kwargs(
    *,
    body: ProjectSourceRefScanRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/projects/scan/source-ref",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> PlanResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlanResponse.from_dict(response.json())

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

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

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
) -> Response[PlanResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ProjectSourceRefScanRequest,
) -> Response[PlanResponse | Problem]:
    """Scan a connected GitHub repository and return a deploy plan.

     Resolves a durable GitHub App installation owned by the authenticated
    account, fetches the selected ref through githubd, and runs the same
    read-only scanner as the multipart upload endpoint. The installation
    token remains inside the control plane. When `install_id` is omitted,
    exactly one connected installation must be able to access `repo`.

    Args:
        body (ProjectSourceRefScanRequest): Connected GitHub repository input for POST
            /v1/projects/scan/source-ref.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlanResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: ProjectSourceRefScanRequest,
) -> PlanResponse | Problem | None:
    """Scan a connected GitHub repository and return a deploy plan.

     Resolves a durable GitHub App installation owned by the authenticated
    account, fetches the selected ref through githubd, and runs the same
    read-only scanner as the multipart upload endpoint. The installation
    token remains inside the control plane. When `install_id` is omitted,
    exactly one connected installation must be able to access `repo`.

    Args:
        body (ProjectSourceRefScanRequest): Connected GitHub repository input for POST
            /v1/projects/scan/source-ref.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlanResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ProjectSourceRefScanRequest,
) -> Response[PlanResponse | Problem]:
    """Scan a connected GitHub repository and return a deploy plan.

     Resolves a durable GitHub App installation owned by the authenticated
    account, fetches the selected ref through githubd, and runs the same
    read-only scanner as the multipart upload endpoint. The installation
    token remains inside the control plane. When `install_id` is omitted,
    exactly one connected installation must be able to access `repo`.

    Args:
        body (ProjectSourceRefScanRequest): Connected GitHub repository input for POST
            /v1/projects/scan/source-ref.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlanResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: ProjectSourceRefScanRequest,
) -> PlanResponse | Problem | None:
    """Scan a connected GitHub repository and return a deploy plan.

     Resolves a durable GitHub App installation owned by the authenticated
    account, fetches the selected ref through githubd, and runs the same
    read-only scanner as the multipart upload endpoint. The installation
    token remains inside the control plane. When `install_id` is omitted,
    exactly one connected installation must be able to access `repo`.

    Args:
        body (ProjectSourceRefScanRequest): Connected GitHub repository input for POST
            /v1/projects/scan/source-ref.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlanResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
