from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.job_artifact_download_response import JobArtifactDownloadResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    name: str,
    id: UUID,
    idx: int,
    artifact: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/jobs/{name}/runs/{id}/tasks/{idx}/artifacts/{artifact}/download".format(
            name=quote(str(name), safe=""),
            id=quote(str(id), safe=""),
            idx=quote(str(idx), safe=""),
            artifact=quote(str(artifact), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> JobArtifactDownloadResponse | Problem | None:
    if response.status_code == 200:
        response_200 = JobArtifactDownloadResponse.from_dict(response.json())

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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[JobArtifactDownloadResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    name: str,
    id: UUID,
    idx: int,
    artifact: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[JobArtifactDownloadResponse | Problem]:
    """Verify a Gregale managed result and obtain a download URL.

     Reads the current obj:// object, checks its size and SHA-256 against the task output manifest, then
    returns a 5-minute signed GET URL. Missing objects return 404; changed bytes return 422.

    Args:
        name (str):
        id (UUID):
        idx (int):
        artifact (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[JobArtifactDownloadResponse | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
        id=id,
        idx=idx,
        artifact=artifact,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    name: str,
    id: UUID,
    idx: int,
    artifact: str,
    *,
    client: AuthenticatedClient | Client,
) -> JobArtifactDownloadResponse | Problem | None:
    """Verify a Gregale managed result and obtain a download URL.

     Reads the current obj:// object, checks its size and SHA-256 against the task output manifest, then
    returns a 5-minute signed GET URL. Missing objects return 404; changed bytes return 422.

    Args:
        name (str):
        id (UUID):
        idx (int):
        artifact (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        JobArtifactDownloadResponse | Problem
    """

    return sync_detailed(
        name=name,
        id=id,
        idx=idx,
        artifact=artifact,
        client=client,
    ).parsed


async def asyncio_detailed(
    name: str,
    id: UUID,
    idx: int,
    artifact: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[JobArtifactDownloadResponse | Problem]:
    """Verify a Gregale managed result and obtain a download URL.

     Reads the current obj:// object, checks its size and SHA-256 against the task output manifest, then
    returns a 5-minute signed GET URL. Missing objects return 404; changed bytes return 422.

    Args:
        name (str):
        id (UUID):
        idx (int):
        artifact (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[JobArtifactDownloadResponse | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
        id=id,
        idx=idx,
        artifact=artifact,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    name: str,
    id: UUID,
    idx: int,
    artifact: str,
    *,
    client: AuthenticatedClient | Client,
) -> JobArtifactDownloadResponse | Problem | None:
    """Verify a Gregale managed result and obtain a download URL.

     Reads the current obj:// object, checks its size and SHA-256 against the task output manifest, then
    returns a 5-minute signed GET URL. Missing objects return 404; changed bytes return 422.

    Args:
        name (str):
        id (UUID):
        idx (int):
        artifact (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        JobArtifactDownloadResponse | Problem
    """

    return (
        await asyncio_detailed(
            name=name,
            id=id,
            idx=idx,
            artifact=artifact,
            client=client,
        )
    ).parsed
