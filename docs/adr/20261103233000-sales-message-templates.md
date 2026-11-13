# ADR: Reusable sales messages with lead personalization

Status: Accepted 2026-11-04

## Context

The sales workspace has leads, conversations, qualification and follow-ups.
Reps need consistent introductions, demo invitations and follow-up messages
that use the correct lead details and assigned salesperson's referral code.
Managers need to approve wording without an external CRM or sending service.

## Decision

Apid owns `sales_message_templates`, excluded from application clones. Managers
create and edit plain-text templates with a title, conversation category,
suggested stage, optional subject and body. Draft and archived templates are
visible only to managers; active templates are available to all configured
sales staff. Activation is the manager's approval of wording. There is no
seeded approval, automatic stage change or outbound sending. Stage is a hint,
so a rep can choose a message appropriate to the actual conversation.

Templates support only `{{lead_name}}`, `{{lead_email}}`, `{{company}}`,
`{{salesperson_email}}` and `{{referral_code}}`. Unknown and malformed double
braces are rejected at save time. Expansion scans the source once and treats
inserted customer values literally. There are no executable expressions,
HTML rendering, template functions or automatic price/commission claims.
A salesperson's email is the reliable staff identity currently available.
Missing values remain marked in the preview and disable its copy button.

Saves compare the template version in the update statement. On conflict the
portal retains the draft, fetches the current record for review and offers an
explicit replacement using that reviewed version. Lists are bounded and
paginated with actor/role-bound encrypted cursors. Reps cannot read a draft
or archive by guessing its ID, and cannot mutate templates.

A preview reads the scoped lead, chosen template version and eligible active
sales code in one repeatable-read transaction. Permissions follow current lead
ownership. Managers use the assigned rep's code, never their own. Previewing
does not create a code. Account status, email verification, MFA enrollment and
configured staff membership gate code availability. Missing, paused or
ineligible codes are reported only when the template requires them.

The copy button revalidates against the server. Changed lead details or
personalized text require another review; changed template versions and
permission changes invalidate the preview. Copy writes only plain text to the
user's clipboard, optionally prefixed with `Subject: `. It does not send or
record a customer interaction. A preview is a point-in-time view; users must
still review and decide where to use the copied message. Audit events contain
IDs, template version and status, never message contents or customer fields.

## Consequences

Apply the additive migration before apid. Routes inherit the existing sales
flag, verified-email and sales-allowlist checks, completed session MFA, session
principal requirement and same-origin write protection. Assets ship with apid.
Older releases ignore this table; rollback does not require dropping it.
The title, subject and body have central UTF-8 byte limits of 120, 200 and 4000.
Referral attribution, commission terms, payments and disabled payouts retain
their existing behavior. No customer website change is needed.
