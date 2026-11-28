from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.task_tool_release_intent_record import TaskToolReleaseIntentRecord
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/task-tool-release-intents/{id}".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | TaskToolReleaseIntentRecord | None:
    if response.status_code == 200:
        response_200 = TaskToolReleaseIntentRecord.from_dict(response.json())

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
) -> Response[Any | TaskToolReleaseIntentRecord]:
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
) -> Response[Any | TaskToolReleaseIntentRecord]:
    """Read the retained Task review and last observed approval; requires account-wide read scope

     Return the retained credential-free Task stack review and its last recorded approval state. The
    requesting key and personal owner may inspect it. This read observes no source files and certifies
    no current binding or native app facts. Expired reviews are projected as expired. Every response
    keeps ready_to_apply and bindings_verified false.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | TaskToolReleaseIntentRecord]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Any | TaskToolReleaseIntentRecord | None:
    """Read the retained Task review and last observed approval; requires account-wide read scope

     Return the retained credential-free Task stack review and its last recorded approval state. The
    requesting key and personal owner may inspect it. This read observes no source files and certifies
    no current binding or native app facts. Expired reviews are projected as expired. Every response
    keeps ready_to_apply and bindings_verified false.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | TaskToolReleaseIntentRecord
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | TaskToolReleaseIntentRecord]:
    """Read the retained Task review and last observed approval; requires account-wide read scope

     Return the retained credential-free Task stack review and its last recorded approval state. The
    requesting key and personal owner may inspect it. This read observes no source files and certifies
    no current binding or native app facts. Expired reviews are projected as expired. Every response
    keeps ready_to_apply and bindings_verified false.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | TaskToolReleaseIntentRecord]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Any | TaskToolReleaseIntentRecord | None:
    """Read the retained Task review and last observed approval; requires account-wide read scope

     Return the retained credential-free Task stack review and its last recorded approval state. The
    requesting key and personal owner may inspect it. This read observes no source files and certifies
    no current binding or native app facts. Expired reviews are projected as expired. Every response
    keeps ready_to_apply and bindings_verified false.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | TaskToolReleaseIntentRecord
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
