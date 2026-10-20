from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_trigger_records_response import ListTriggerRecordsResponse
from ...models.problem import Problem
from ...models.trigger_record_state import TriggerRecordState
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: str,
    *,
    state: TriggerRecordState | Unset = UNSET,
    limit: int | Unset = 50,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_state: str | Unset = UNSET
    if not isinstance(state, Unset):
        json_state = state

    params["state"] = json_state

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/triggers/{id}/records".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListTriggerRecordsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ListTriggerRecordsResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ListTriggerRecordsResponse | Problem]:
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
    state: TriggerRecordState | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[ListTriggerRecordsResponse | Problem]:
    r"""List records for one trigger.

     Newest-first by `received_at`. The dashboard uses this to
    build the \"Recent fires\" view; the CLI uses it for `--tail`.

    Args:
        id (str):
        state (TriggerRecordState | Unset): Lifecycle of one trigger record.
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListTriggerRecordsResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        state=state,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    state: TriggerRecordState | Unset = UNSET,
    limit: int | Unset = 50,
) -> ListTriggerRecordsResponse | Problem | None:
    r"""List records for one trigger.

     Newest-first by `received_at`. The dashboard uses this to
    build the \"Recent fires\" view; the CLI uses it for `--tail`.

    Args:
        id (str):
        state (TriggerRecordState | Unset): Lifecycle of one trigger record.
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListTriggerRecordsResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        state=state,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    state: TriggerRecordState | Unset = UNSET,
    limit: int | Unset = 50,
) -> Response[ListTriggerRecordsResponse | Problem]:
    r"""List records for one trigger.

     Newest-first by `received_at`. The dashboard uses this to
    build the \"Recent fires\" view; the CLI uses it for `--tail`.

    Args:
        id (str):
        state (TriggerRecordState | Unset): Lifecycle of one trigger record.
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListTriggerRecordsResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        state=state,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    state: TriggerRecordState | Unset = UNSET,
    limit: int | Unset = 50,
) -> ListTriggerRecordsResponse | Problem | None:
    r"""List records for one trigger.

     Newest-first by `received_at`. The dashboard uses this to
    build the \"Recent fires\" view; the CLI uses it for `--tail`.

    Args:
        id (str):
        state (TriggerRecordState | Unset): Lifecycle of one trigger record.
        limit (int | Unset):  Default: 50.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListTriggerRecordsResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            state=state,
            limit=limit,
        )
    ).parsed
