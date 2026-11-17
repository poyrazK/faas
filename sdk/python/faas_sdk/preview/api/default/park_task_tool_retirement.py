from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.task_tool_release_intent_record import TaskToolReleaseIntentRecord
from ...models.task_tool_retirement_request import TaskToolRetirementRequest
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: TaskToolRetirementRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/task-tool-release-intents/{id}/recovery/retire".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | Problem | TaskToolReleaseIntentRecord | None:
    if response.status_code == 200:
        response_200 = TaskToolReleaseIntentRecord.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[Any | Problem | TaskToolReleaseIntentRecord]:
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
    body: TaskToolRetirementRequest,
) -> Response[Any | Problem | TaskToolReleaseIntentRecord]:
    """Request conditional parking of the exact quarantined worker; requires the requesting key and MFA
    policy

     The trusted local CLI observes the retained candidate claim fences and rechecks replacement workers,
    restored web, native policy, observer, running generation and shutdown grace. Native parking and
    separate retirement approval consumption commit together. Pending replays only reconcile the
    recorded transition. Retired requires native drain plus absent or expired Task registrations
    observed through the read-only observer role. Customer Task SQL and native state do not share a
    transaction. Active Tasks are not cancelled and historical receipts do not certify current service
    health.

    Args:
        id (str):
        body (TaskToolRetirementRequest): Private trusted CLI qualification, native park or read-
            only shutdown observation. Agent tools accept public identities only. Pending replays
            never issue a new park request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | TaskToolReleaseIntentRecord]
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
    body: TaskToolRetirementRequest,
) -> Any | Problem | TaskToolReleaseIntentRecord | None:
    """Request conditional parking of the exact quarantined worker; requires the requesting key and MFA
    policy

     The trusted local CLI observes the retained candidate claim fences and rechecks replacement workers,
    restored web, native policy, observer, running generation and shutdown grace. Native parking and
    separate retirement approval consumption commit together. Pending replays only reconcile the
    recorded transition. Retired requires native drain plus absent or expired Task registrations
    observed through the read-only observer role. Customer Task SQL and native state do not share a
    transaction. Active Tasks are not cancelled and historical receipts do not certify current service
    health.

    Args:
        id (str):
        body (TaskToolRetirementRequest): Private trusted CLI qualification, native park or read-
            only shutdown observation. Agent tools accept public identities only. Pending replays
            never issue a new park request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | TaskToolReleaseIntentRecord
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
    body: TaskToolRetirementRequest,
) -> Response[Any | Problem | TaskToolReleaseIntentRecord]:
    """Request conditional parking of the exact quarantined worker; requires the requesting key and MFA
    policy

     The trusted local CLI observes the retained candidate claim fences and rechecks replacement workers,
    restored web, native policy, observer, running generation and shutdown grace. Native parking and
    separate retirement approval consumption commit together. Pending replays only reconcile the
    recorded transition. Retired requires native drain plus absent or expired Task registrations
    observed through the read-only observer role. Customer Task SQL and native state do not share a
    transaction. Active Tasks are not cancelled and historical receipts do not certify current service
    health.

    Args:
        id (str):
        body (TaskToolRetirementRequest): Private trusted CLI qualification, native park or read-
            only shutdown observation. Agent tools accept public identities only. Pending replays
            never issue a new park request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | TaskToolReleaseIntentRecord]
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
    body: TaskToolRetirementRequest,
) -> Any | Problem | TaskToolReleaseIntentRecord | None:
    """Request conditional parking of the exact quarantined worker; requires the requesting key and MFA
    policy

     The trusted local CLI observes the retained candidate claim fences and rechecks replacement workers,
    restored web, native policy, observer, running generation and shutdown grace. Native parking and
    separate retirement approval consumption commit together. Pending replays only reconcile the
    recorded transition. Retired requires native drain plus absent or expired Task registrations
    observed through the read-only observer role. Customer Task SQL and native state do not share a
    transaction. Active Tasks are not cancelled and historical receipts do not certify current service
    health.

    Args:
        id (str):
        body (TaskToolRetirementRequest): Private trusted CLI qualification, native park or read-
            only shutdown observation. Agent tools accept public identities only. Pending replays
            never issue a new park request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | TaskToolReleaseIntentRecord
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
