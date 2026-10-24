from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.obs_alert_triage_request import ObsAlertTriageRequest
from ...models.obs_alert_triage_response import ObsAlertTriageResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: ObsAlertTriageRequest,
    idempotency_key: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/admin/obs/incidents/platform_alert/{id}/triage".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ObsAlertTriageResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ObsAlertTriageResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = cast(Any, None)
        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ObsAlertTriageResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient,
    body: ObsAlertTriageRequest,
    idempotency_key: str,
) -> Response[Any | ObsAlertTriageResponse | Problem]:
    """Update human triage on one immutable retained alert episode.

     Requires an allowlisted operator session, MFA with recent step-up,
    trusted browser origin and Idempotency-Key. Bearer keys cannot mutate.
    Series aliases cannot be mutated. Every accepted action increments the
    episode revision and commits with its audit entry. Claims cannot take
    over another operator's ownership. Snooze affects inbox triage only;
    observed firing/resolved/unknown status and notification delivery remain
    independent. Obtain the current revision from incident evidence.

    Args:
        id (str):
        idempotency_key (str):
        body (ObsAlertTriageRequest): One revision-checked triage action on an exact retained
            episode ID.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsAlertTriageResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient,
    body: ObsAlertTriageRequest,
    idempotency_key: str,
) -> Any | ObsAlertTriageResponse | Problem | None:
    """Update human triage on one immutable retained alert episode.

     Requires an allowlisted operator session, MFA with recent step-up,
    trusted browser origin and Idempotency-Key. Bearer keys cannot mutate.
    Series aliases cannot be mutated. Every accepted action increments the
    episode revision and commits with its audit entry. Claims cannot take
    over another operator's ownership. Snooze affects inbox triage only;
    observed firing/resolved/unknown status and notification delivery remain
    independent. Obtain the current revision from incident evidence.

    Args:
        id (str):
        idempotency_key (str):
        body (ObsAlertTriageRequest): One revision-checked triage action on an exact retained
            episode ID.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsAlertTriageResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient,
    body: ObsAlertTriageRequest,
    idempotency_key: str,
) -> Response[Any | ObsAlertTriageResponse | Problem]:
    """Update human triage on one immutable retained alert episode.

     Requires an allowlisted operator session, MFA with recent step-up,
    trusted browser origin and Idempotency-Key. Bearer keys cannot mutate.
    Series aliases cannot be mutated. Every accepted action increments the
    episode revision and commits with its audit entry. Claims cannot take
    over another operator's ownership. Snooze affects inbox triage only;
    observed firing/resolved/unknown status and notification delivery remain
    independent. Obtain the current revision from incident evidence.

    Args:
        id (str):
        idempotency_key (str):
        body (ObsAlertTriageRequest): One revision-checked triage action on an exact retained
            episode ID.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsAlertTriageResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient,
    body: ObsAlertTriageRequest,
    idempotency_key: str,
) -> Any | ObsAlertTriageResponse | Problem | None:
    """Update human triage on one immutable retained alert episode.

     Requires an allowlisted operator session, MFA with recent step-up,
    trusted browser origin and Idempotency-Key. Bearer keys cannot mutate.
    Series aliases cannot be mutated. Every accepted action increments the
    episode revision and commits with its audit entry. Claims cannot take
    over another operator's ownership. Snooze affects inbox triage only;
    observed firing/resolved/unknown status and notification delivery remain
    independent. Obtain the current revision from incident evidence.

    Args:
        id (str):
        idempotency_key (str):
        body (ObsAlertTriageRequest): One revision-checked triage action on an exact retained
            episode ID.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsAlertTriageResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
