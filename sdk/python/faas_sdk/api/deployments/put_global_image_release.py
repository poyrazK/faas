from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.global_image_release_response import GlobalImageReleaseResponse
from ...models.problem import Problem
from ...models.put_global_image_release_request import PutGlobalImageReleaseRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    release_id: UUID,
    *,
    body: PutGlobalImageReleaseRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/global-apps/{slug}/image-releases/{release_id}".format(
            slug=quote(str(slug), safe=""),
            release_id=quote(str(release_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    _kwargs["follow_redirects"] = False
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> GlobalImageReleaseResponse | Problem | None:
    if response.status_code == 202:
        response_202 = GlobalImageReleaseResponse.from_dict(response.json())

        return response_202

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
) -> Response[GlobalImageReleaseResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    release_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutGlobalImageReleaseRequest,
) -> Response[GlobalImageReleaseResponse | Problem]:
    """Allocate and resume an image release in every selected region.

     Requires personal directory-account deploy-write access and verified email.
    Persists an immutable image, increasing generation and complete verified
    target snapshot before regional I/O. Keys indexed by region authorize
    this request only and are never retained. They establish six-day scoped
    consent sealed by directory apid before registration. Background recovery
    and later releases reuse active consent without admin keys; missing or
    expired consent requires renewed keys. Matching UUID/image retries keep
    the original snapshot. Missing sealing identities fail closed.
    Redirects are refused. The request is bounded to 16384 bytes and 90 seconds,
    with at most 32 targets and four concurrent regions. Timeouts remain
    pending; retry the same UUID/image to recover regional acceptance receipts.
    A newer generation supersedes older work. Accepted records intent in every
    snapshotted region; readiness, quota and ingress are separate capabilities.

    Args:
        slug (str):
        release_id (UUID):
        body (PutGlobalImageReleaseRequest): Global image release with sealed scoped consent and
            background recovery. Regional admin keys are never stored or returned.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GlobalImageReleaseResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        release_id=release_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    release_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutGlobalImageReleaseRequest,
) -> GlobalImageReleaseResponse | Problem | None:
    """Allocate and resume an image release in every selected region.

     Requires personal directory-account deploy-write access and verified email.
    Persists an immutable image, increasing generation and complete verified
    target snapshot before regional I/O. Keys indexed by region authorize
    this request only and are never retained. They establish six-day scoped
    consent sealed by directory apid before registration. Background recovery
    and later releases reuse active consent without admin keys; missing or
    expired consent requires renewed keys. Matching UUID/image retries keep
    the original snapshot. Missing sealing identities fail closed.
    Redirects are refused. The request is bounded to 16384 bytes and 90 seconds,
    with at most 32 targets and four concurrent regions. Timeouts remain
    pending; retry the same UUID/image to recover regional acceptance receipts.
    A newer generation supersedes older work. Accepted records intent in every
    snapshotted region; readiness, quota and ingress are separate capabilities.

    Args:
        slug (str):
        release_id (UUID):
        body (PutGlobalImageReleaseRequest): Global image release with sealed scoped consent and
            background recovery. Regional admin keys are never stored or returned.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GlobalImageReleaseResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        release_id=release_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    release_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutGlobalImageReleaseRequest,
) -> Response[GlobalImageReleaseResponse | Problem]:
    """Allocate and resume an image release in every selected region.

     Requires personal directory-account deploy-write access and verified email.
    Persists an immutable image, increasing generation and complete verified
    target snapshot before regional I/O. Keys indexed by region authorize
    this request only and are never retained. They establish six-day scoped
    consent sealed by directory apid before registration. Background recovery
    and later releases reuse active consent without admin keys; missing or
    expired consent requires renewed keys. Matching UUID/image retries keep
    the original snapshot. Missing sealing identities fail closed.
    Redirects are refused. The request is bounded to 16384 bytes and 90 seconds,
    with at most 32 targets and four concurrent regions. Timeouts remain
    pending; retry the same UUID/image to recover regional acceptance receipts.
    A newer generation supersedes older work. Accepted records intent in every
    snapshotted region; readiness, quota and ingress are separate capabilities.

    Args:
        slug (str):
        release_id (UUID):
        body (PutGlobalImageReleaseRequest): Global image release with sealed scoped consent and
            background recovery. Regional admin keys are never stored or returned.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GlobalImageReleaseResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        release_id=release_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    release_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutGlobalImageReleaseRequest,
) -> GlobalImageReleaseResponse | Problem | None:
    """Allocate and resume an image release in every selected region.

     Requires personal directory-account deploy-write access and verified email.
    Persists an immutable image, increasing generation and complete verified
    target snapshot before regional I/O. Keys indexed by region authorize
    this request only and are never retained. They establish six-day scoped
    consent sealed by directory apid before registration. Background recovery
    and later releases reuse active consent without admin keys; missing or
    expired consent requires renewed keys. Matching UUID/image retries keep
    the original snapshot. Missing sealing identities fail closed.
    Redirects are refused. The request is bounded to 16384 bytes and 90 seconds,
    with at most 32 targets and four concurrent regions. Timeouts remain
    pending; retry the same UUID/image to recover regional acceptance receipts.
    A newer generation supersedes older work. Accepted records intent in every
    snapshotted region; readiness, quota and ingress are separate capabilities.

    Args:
        slug (str):
        release_id (UUID):
        body (PutGlobalImageReleaseRequest): Global image release with sealed scoped consent and
            background recovery. Regional admin keys are never stored or returned.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GlobalImageReleaseResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            release_id=release_id,
            client=client,
            body=body,
        )
    ).parsed
