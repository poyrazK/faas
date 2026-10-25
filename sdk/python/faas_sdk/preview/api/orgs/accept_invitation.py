from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.org_member_response import OrgMemberResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    token: str,
    *,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/invitations/{token}/accept".format(
            token=quote(str(token), safe=""),
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OrgMemberResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OrgMemberResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OrgMemberResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    token: str,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> Response[OrgMemberResponse | Problem]:
    """Accept an invitation token (consume + add as member).

     Consumes the invitation via `Store.ConsumeOrgInvitation` —
    the load-bearing tx stamps `consumed_at`, inserts the active
    membership, and reads the live member cap (PR 2 cap-in-tx
    back-stop; documents at `pkg/state/memstore.go`). Two audit
    rows fire post-mutation per ADR-035: `org.invitation.accepted`
    (invitation-side record) and `org.member.added` (member-side
    record). The bearer must have a valid session or API key but
    no `X-Active-Org` — the invitation IS how they get one. PR 8
    adds step-up at accept time.

    Args:
        token (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgMemberResponse | Problem]
    """

    kwargs = _get_kwargs(
        token=token,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    token: str,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> OrgMemberResponse | Problem | None:
    """Accept an invitation token (consume + add as member).

     Consumes the invitation via `Store.ConsumeOrgInvitation` —
    the load-bearing tx stamps `consumed_at`, inserts the active
    membership, and reads the live member cap (PR 2 cap-in-tx
    back-stop; documents at `pkg/state/memstore.go`). Two audit
    rows fire post-mutation per ADR-035: `org.invitation.accepted`
    (invitation-side record) and `org.member.added` (member-side
    record). The bearer must have a valid session or API key but
    no `X-Active-Org` — the invitation IS how they get one. PR 8
    adds step-up at accept time.

    Args:
        token (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgMemberResponse | Problem
    """

    return sync_detailed(
        token=token,
        client=client,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    token: str,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> Response[OrgMemberResponse | Problem]:
    """Accept an invitation token (consume + add as member).

     Consumes the invitation via `Store.ConsumeOrgInvitation` —
    the load-bearing tx stamps `consumed_at`, inserts the active
    membership, and reads the live member cap (PR 2 cap-in-tx
    back-stop; documents at `pkg/state/memstore.go`). Two audit
    rows fire post-mutation per ADR-035: `org.invitation.accepted`
    (invitation-side record) and `org.member.added` (member-side
    record). The bearer must have a valid session or API key but
    no `X-Active-Org` — the invitation IS how they get one. PR 8
    adds step-up at accept time.

    Args:
        token (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgMemberResponse | Problem]
    """

    kwargs = _get_kwargs(
        token=token,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    token: str,
    *,
    client: AuthenticatedClient | Client,
    idempotency_key: str | Unset = UNSET,
) -> OrgMemberResponse | Problem | None:
    """Accept an invitation token (consume + add as member).

     Consumes the invitation via `Store.ConsumeOrgInvitation` —
    the load-bearing tx stamps `consumed_at`, inserts the active
    membership, and reads the live member cap (PR 2 cap-in-tx
    back-stop; documents at `pkg/state/memstore.go`). Two audit
    rows fire post-mutation per ADR-035: `org.invitation.accepted`
    (invitation-side record) and `org.member.added` (member-side
    record). The bearer must have a valid session or API key but
    no `X-Active-Org` — the invitation IS how they get one. PR 8
    adds step-up at accept time.

    Args:
        token (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgMemberResponse | Problem
    """

    return (
        await asyncio_detailed(
            token=token,
            client=client,
            idempotency_key=idempotency_key,
        )
    ).parsed
