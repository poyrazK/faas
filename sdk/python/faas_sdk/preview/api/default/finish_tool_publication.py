from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.tool_publication_outcome import ToolPublicationOutcome
from ...models.tool_publication_record import ToolPublicationRecord
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: ToolPublicationOutcome,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/tool-publications/{id}/outcome".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ToolPublicationRecord | None:
    if response.status_code == 200:
        response_200 = ToolPublicationRecord.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ToolPublicationRecord]:
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
    body: ToolPublicationOutcome,
) -> Response[Any | ToolPublicationRecord]:
    """Retain immutable publication evidence; requires deploy-write scope and MFA policy

     Publisher-attested runtime catalogs and tests are bound to a native owned serving deployment and
    source archive. Identical terminal writes are idempotent. This endpoint executes no tools.

    Args:
        id (str):
        body (ToolPublicationOutcome): Immutable publisher-attested evidence bounded to 512 KiB.
            Completed outcomes require the complete observed catalog and exact passing tests. Failed
            or uncertain outcomes contain only status and protocol_verified=false.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ToolPublicationRecord]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ToolPublicationOutcome,
) -> Any | ToolPublicationRecord | None:
    """Retain immutable publication evidence; requires deploy-write scope and MFA policy

     Publisher-attested runtime catalogs and tests are bound to a native owned serving deployment and
    source archive. Identical terminal writes are idempotent. This endpoint executes no tools.

    Args:
        id (str):
        body (ToolPublicationOutcome): Immutable publisher-attested evidence bounded to 512 KiB.
            Completed outcomes require the complete observed catalog and exact passing tests. Failed
            or uncertain outcomes contain only status and protocol_verified=false.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ToolPublicationRecord
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ToolPublicationOutcome,
) -> Response[Any | ToolPublicationRecord]:
    """Retain immutable publication evidence; requires deploy-write scope and MFA policy

     Publisher-attested runtime catalogs and tests are bound to a native owned serving deployment and
    source archive. Identical terminal writes are idempotent. This endpoint executes no tools.

    Args:
        id (str):
        body (ToolPublicationOutcome): Immutable publisher-attested evidence bounded to 512 KiB.
            Completed outcomes require the complete observed catalog and exact passing tests. Failed
            or uncertain outcomes contain only status and protocol_verified=false.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ToolPublicationRecord]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ToolPublicationOutcome,
) -> Any | ToolPublicationRecord | None:
    """Retain immutable publication evidence; requires deploy-write scope and MFA policy

     Publisher-attested runtime catalogs and tests are bound to a native owned serving deployment and
    source archive. Identical terminal writes are idempotent. This endpoint executes no tools.

    Args:
        id (str):
        body (ToolPublicationOutcome): Immutable publisher-attested evidence bounded to 512 KiB.
            Completed outcomes require the complete observed catalog and exact passing tests. Failed
            or uncertain outcomes contain only status and protocol_verified=false.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ToolPublicationRecord
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
