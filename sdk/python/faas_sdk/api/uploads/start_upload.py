from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.upload_start_request import UploadStartRequest
from ...models.upload_start_response import UploadStartResponse
from ...types import Response


def _get_kwargs(
    *,
    body: UploadStartRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/uploads",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | UploadStartResponse | None:
    if response.status_code == 201:
        response_201 = UploadStartResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | UploadStartResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: UploadStartRequest,
) -> Response[Problem | UploadStartResponse]:
    """Open a resumable upload session.

    Args:
        body (UploadStartRequest): Body of POST /v1/uploads. `total_size` must be ≤ the per-plan
            SourceTarballMaxMB cap (Free/Hobby 100 MB, Pro/Scale 250 MB); the handler returns 413 +
            `source_too_large` otherwise. `sha256_hex` is recorded for the build_provenance audit row
            only — the server does NOT re-verify it at commit time (ADR-115 trust boundary).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | UploadStartResponse]
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
    body: UploadStartRequest,
) -> Problem | UploadStartResponse | None:
    """Open a resumable upload session.

    Args:
        body (UploadStartRequest): Body of POST /v1/uploads. `total_size` must be ≤ the per-plan
            SourceTarballMaxMB cap (Free/Hobby 100 MB, Pro/Scale 250 MB); the handler returns 413 +
            `source_too_large` otherwise. `sha256_hex` is recorded for the build_provenance audit row
            only — the server does NOT re-verify it at commit time (ADR-115 trust boundary).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | UploadStartResponse
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: UploadStartRequest,
) -> Response[Problem | UploadStartResponse]:
    """Open a resumable upload session.

    Args:
        body (UploadStartRequest): Body of POST /v1/uploads. `total_size` must be ≤ the per-plan
            SourceTarballMaxMB cap (Free/Hobby 100 MB, Pro/Scale 250 MB); the handler returns 413 +
            `source_too_large` otherwise. `sha256_hex` is recorded for the build_provenance audit row
            only — the server does NOT re-verify it at commit time (ADR-115 trust boundary).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | UploadStartResponse]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: UploadStartRequest,
) -> Problem | UploadStartResponse | None:
    """Open a resumable upload session.

    Args:
        body (UploadStartRequest): Body of POST /v1/uploads. `total_size` must be ≤ the per-plan
            SourceTarballMaxMB cap (Free/Hobby 100 MB, Pro/Scale 250 MB); the handler returns 413 +
            `source_too_large` otherwise. `sha256_hex` is recorded for the build_provenance audit row
            only — the server does NOT re-verify it at commit time (ADR-115 trust boundary).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | UploadStartResponse
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
