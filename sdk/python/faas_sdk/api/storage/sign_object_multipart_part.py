from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_multipart_part_sign_request import ObjectMultipartPartSignRequest
from ...models.object_signed_request import ObjectSignedRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    upload: UUID,
    part: int,
    *,
    body: ObjectMultipartPartSignRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/buckets/{bucket}/multipart-uploads/{upload}/parts/{part}/signed-url".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
            upload=quote(str(upload), safe=""),
            part=quote(str(part), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ObjectSignedRequest | Problem:
    if response.status_code == 200:
        response_200 = ObjectSignedRequest.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectSignedRequest | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    bucket: UUID,
    upload: UUID,
    part: int,
    *,
    client: AuthenticatedClient | Client,
    body: ObjectMultipartPartSignRequest,
) -> Response[ObjectSignedRequest | Problem]:
    """Issue an exact-length direct upload URL for one part

     Requires storage:write or admin and a matching bucket grant. The URL
    binds the server-calculated byte length for this part and expires within
    15 minutes. Upload it without Gregale credentials and retain the ETag
    response header for completion. Every issued part URL consumes the
    authorization safety budget.

    Args:
        slug (str):
        bucket (UUID):
        upload (UUID):
        part (int):
        body (ObjectMultipartPartSignRequest): Requested lifetime for one exact-length part
            capability.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectSignedRequest | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        upload=upload,
        part=part,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    bucket: UUID,
    upload: UUID,
    part: int,
    *,
    client: AuthenticatedClient | Client,
    body: ObjectMultipartPartSignRequest,
) -> ObjectSignedRequest | Problem | None:
    """Issue an exact-length direct upload URL for one part

     Requires storage:write or admin and a matching bucket grant. The URL
    binds the server-calculated byte length for this part and expires within
    15 minutes. Upload it without Gregale credentials and retain the ETag
    response header for completion. Every issued part URL consumes the
    authorization safety budget.

    Args:
        slug (str):
        bucket (UUID):
        upload (UUID):
        part (int):
        body (ObjectMultipartPartSignRequest): Requested lifetime for one exact-length part
            capability.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectSignedRequest | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        upload=upload,
        part=part,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    upload: UUID,
    part: int,
    *,
    client: AuthenticatedClient | Client,
    body: ObjectMultipartPartSignRequest,
) -> Response[ObjectSignedRequest | Problem]:
    """Issue an exact-length direct upload URL for one part

     Requires storage:write or admin and a matching bucket grant. The URL
    binds the server-calculated byte length for this part and expires within
    15 minutes. Upload it without Gregale credentials and retain the ETag
    response header for completion. Every issued part URL consumes the
    authorization safety budget.

    Args:
        slug (str):
        bucket (UUID):
        upload (UUID):
        part (int):
        body (ObjectMultipartPartSignRequest): Requested lifetime for one exact-length part
            capability.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectSignedRequest | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        upload=upload,
        part=part,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    bucket: UUID,
    upload: UUID,
    part: int,
    *,
    client: AuthenticatedClient | Client,
    body: ObjectMultipartPartSignRequest,
) -> ObjectSignedRequest | Problem | None:
    """Issue an exact-length direct upload URL for one part

     Requires storage:write or admin and a matching bucket grant. The URL
    binds the server-calculated byte length for this part and expires within
    15 minutes. Upload it without Gregale credentials and retain the ETag
    response header for completion. Every issued part URL consumes the
    authorization safety budget.

    Args:
        slug (str):
        bucket (UUID):
        upload (UUID):
        part (int):
        body (ObjectMultipartPartSignRequest): Requested lifetime for one exact-length part
            capability.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectSignedRequest | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            upload=upload,
            part=part,
            client=client,
            body=body,
        )
    ).parsed
