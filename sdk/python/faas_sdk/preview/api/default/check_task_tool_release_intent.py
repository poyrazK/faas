from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.task_tool_release_intent_check import TaskToolReleaseIntentCheck
from ...models.task_tool_release_intent_record import TaskToolReleaseIntentRecord
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: TaskToolReleaseIntentCheck,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/task-tool-release-intents/{id}/check".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
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
    body: TaskToolReleaseIntentCheck,
) -> Response[Any | TaskToolReleaseIntentRecord]:
    """Revalidate source fingerprint and native app reviews; requires deploy-write scope and MFA policy

     Compare a freshly computed local release fingerprint with the retained Task intent and revalidate
    native app and requesting-key facts. Observed mismatches permanently mark pending or approved
    reviews stale. Restoring previous source bytes does not revive approval. An execution request
    atomically claims or guards a separately owner-authorized local operation; only its requesting key
    may transition it. Guard checks retain app configuration pins while allowing deployment revisions
    made by the native adapter. Existing native traffic/readiness fences remain mandatory. This endpoint
    only changes journal metadata and never executes customer code itself.

    Args:
        id (str):
        body (TaskToolReleaseIntentCheck): Fresh local fingerprint for drift detection or a
            separately authorized adapter claim transition.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | TaskToolReleaseIntentRecord]
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
    body: TaskToolReleaseIntentCheck,
) -> Any | TaskToolReleaseIntentRecord | None:
    """Revalidate source fingerprint and native app reviews; requires deploy-write scope and MFA policy

     Compare a freshly computed local release fingerprint with the retained Task intent and revalidate
    native app and requesting-key facts. Observed mismatches permanently mark pending or approved
    reviews stale. Restoring previous source bytes does not revive approval. An execution request
    atomically claims or guards a separately owner-authorized local operation; only its requesting key
    may transition it. Guard checks retain app configuration pins while allowing deployment revisions
    made by the native adapter. Existing native traffic/readiness fences remain mandatory. This endpoint
    only changes journal metadata and never executes customer code itself.

    Args:
        id (str):
        body (TaskToolReleaseIntentCheck): Fresh local fingerprint for drift detection or a
            separately authorized adapter claim transition.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | TaskToolReleaseIntentRecord
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
    body: TaskToolReleaseIntentCheck,
) -> Response[Any | TaskToolReleaseIntentRecord]:
    """Revalidate source fingerprint and native app reviews; requires deploy-write scope and MFA policy

     Compare a freshly computed local release fingerprint with the retained Task intent and revalidate
    native app and requesting-key facts. Observed mismatches permanently mark pending or approved
    reviews stale. Restoring previous source bytes does not revive approval. An execution request
    atomically claims or guards a separately owner-authorized local operation; only its requesting key
    may transition it. Guard checks retain app configuration pins while allowing deployment revisions
    made by the native adapter. Existing native traffic/readiness fences remain mandatory. This endpoint
    only changes journal metadata and never executes customer code itself.

    Args:
        id (str):
        body (TaskToolReleaseIntentCheck): Fresh local fingerprint for drift detection or a
            separately authorized adapter claim transition.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | TaskToolReleaseIntentRecord]
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
    body: TaskToolReleaseIntentCheck,
) -> Any | TaskToolReleaseIntentRecord | None:
    """Revalidate source fingerprint and native app reviews; requires deploy-write scope and MFA policy

     Compare a freshly computed local release fingerprint with the retained Task intent and revalidate
    native app and requesting-key facts. Observed mismatches permanently mark pending or approved
    reviews stale. Restoring previous source bytes does not revive approval. An execution request
    atomically claims or guards a separately owner-authorized local operation; only its requesting key
    may transition it. Guard checks retain app configuration pins while allowing deployment revisions
    made by the native adapter. Existing native traffic/readiness fences remain mandatory. This endpoint
    only changes journal metadata and never executes customer code itself.

    Args:
        id (str):
        body (TaskToolReleaseIntentCheck): Fresh local fingerprint for drift detection or a
            separately authorized adapter claim transition.

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
            body=body,
        )
    ).parsed
