import {sharedOrderViews,createSharedViewForm} from './shared-views.js'
import type {SharedView} from './shared-views.js'
import {orderOperations,createOrderViewForm,createBulkFulfilmentForm,parseCriteria} from './order-operations.js'
import type {Criteria,View,OrderCursor,Preview} from './order-operations.js'
import {webhookDiagnostics,createWebhookTestForm} from './webhook-diagnostics.js'
import { webhookSubscriptions, createSubscriptionForm } from './subscriptions.js'
import type { Subscription } from './subscriptions.js'
import { deliveryControls, createDeliveryControlForm } from './delivery-controls.js'
import type { Health, ReviewedDelivery } from './delivery-controls.js'
import { webhookDeliveries, createReplayForm } from './deliveries.js'
import { createWorkflowForm, orderWorkflow } from './workflows.js'
import { parseCsv, previewCsv, createImportForm, orderImports, exportOrders } from './imports.js'
import type { ImportPreview } from './imports.js'
import { teamInvitations, createInvitationForm, invitationLink, readInvitationLink } from './invitations.js'
import { teamAdministration, createMembershipForm } from './administration.js'
import type { ManagedMember, AuditCursor, TeamRole } from './administration.js'
import { teamsClient, createOrderForm } from './orders.js'
import type { Order, Status, Membership } from './orders.js'
const input = (id: string) => document.getElementById(id) as HTMLInputElement
const select = (id: string) => document.getElementById(id) as HTMLSelectElement
const button = (id: string) => document.getElementById(id) as HTMLButtonElement
const text = (id: string, message: string) => { document.getElementById(id)!.textContent = message }
let client: ReturnType<typeof teamsClient> | null = null
let form: ReturnType<typeof createOrderForm> | null = null
let membership: Membership | null = null, editing: Order | null = null
let administration: ReturnType<typeof teamAdministration> | null = null, memberForm: ReturnType<typeof createMembershipForm> | null = null
let editingMember: ManagedMember | null = null, memberNext: string | null = null, auditNext: AuditCursor | null = null
let inviteForm: ReturnType<typeof createInvitationForm> | null = null, inviteNext: string | null = null
let importForm: ReturnType<typeof createImportForm> | null = null, importPreview: ImportPreview | null = null, importsNext: string | null = null, importSource = 'pasted-orders.csv'
let workflowForm: ReturnType<typeof createWorkflowForm> | null = null, historyOrder: string | null = null, historyNext: number | null = null
let replayForm: ReturnType<typeof createReplayForm> | null = null, deliveryNext: string | null = null, attemptEvent: string | null = null, attemptNext: number | null = null
let controlForm: ReturnType<typeof createDeliveryControlForm> | null = null, queueHealth: Health | null = null, batchReviewed: readonly ReviewedDelivery[] | null = null, operationsNext: string | null = null
let subscriptionForm: ReturnType<typeof createSubscriptionForm> | null = null, selectedSubscription: Subscription | null = null, subscriptionNext: string | null = null, subscriptionReplay: ReturnType<typeof createReplayForm> | null = null
let testForm: ReturnType<typeof createWebhookTestForm> | null = null, subscriptionDeliveryNext: string | null = null, subscriptionAttemptEvent: string | null = null, subscriptionAttemptNext: number | null = null, subscriptionHistoryNext: string | null = null
let viewForm: ReturnType<typeof createOrderViewForm> | null = null, bulkForm: ReturnType<typeof createBulkFulfilmentForm> | null = null, editingView: View | null = null, viewsNext: string | null = null, bulkHistoryNext: string | null = null, bulkReviewed: Preview[] | null = null
let sharedForm: ReturnType<typeof createSharedViewForm> | null = null, selectedShared: SharedView | null = null, sharedNext: string | null = null, sharedHistoryNext: string | null = null
const bulkSelected = new Map<string,Order>()
let next: OrderCursor | null = null, actionToken = '', busy = false
const canWrite = () => membership?.role === 'owner' || membership?.role === 'admin'
function controls() {
  const pending = Boolean(form?.pending || memberForm?.pending || inviteForm?.pending || importForm?.pending || workflowForm?.pending || replayForm?.pending || controlForm?.pending || subscriptionForm?.pending || subscriptionReplay?.pending || testForm?.pending || viewForm?.pending || bulkForm?.pending || sharedForm?.pending)
  button('shared-views-load').disabled = busy || pending || !client
  button('shared-views-next').disabled = busy || pending || !client || !sharedNext
  for (const name of ['shared-view-save','shared-view-new']) button(name).disabled = busy || pending || !canWrite()
  button('shared-view-copy').disabled = busy || pending || !client || !selectedShared
  button('shared-view-resolve').disabled = busy || !sharedForm?.pending
  button('shared-view-history').disabled = busy || pending || !client || !selectedShared
  button('shared-view-history-next').disabled = busy || pending || !client || !selectedShared || !sharedHistoryNext
  input('shared-view-name').disabled = busy || pending || !canWrite()
  input('shared-copy-name').disabled = busy || pending || !client || !selectedShared
  for (const element of document.querySelectorAll<HTMLButtonElement>('[data-shared-view]')) element.disabled = busy || pending || !client
  for (const name of ['views-load','view-save','view-new','bulk-history']) button(name).disabled = busy || pending || !client
  button('views-next').disabled = busy || pending || !client || !viewsNext
  button('view-resolve').disabled = busy || !viewForm?.pending
  button('bulk-preview').disabled = busy || pending || !canWrite() || !bulkSelected.size
  button('bulk-apply').disabled = busy || pending || !canWrite() || !bulkReviewed?.length || bulkReviewed.some(r => r.reason !== 'eligible') || !input('bulk-confirm').checked
  button('bulk-resolve').disabled = busy || !bulkForm?.pending
  button('bulk-history-next').disabled = busy || pending || !client || !bulkHistoryNext
  input('bulk-confirm').disabled = busy || pending || !canWrite() || !bulkReviewed?.length
  for (const name of ['view-name','view-min','view-max']) input(name).disabled = busy || pending
  select('view-sort').disabled = busy || pending
  for (const element of document.querySelectorAll<HTMLButtonElement>('[data-saved-view]')) element.disabled = busy || pending || !client
  for (const element of document.querySelectorAll<HTMLInputElement>('[data-bulk-order]')) element.disabled = busy || pending || !canWrite() || element.dataset.status !== 'submitted'
  for (const name of ['import-start','import-advance','imports-load']) button(name).disabled = busy || pending || !canWrite()
  button('import-start').disabled ||= !importPreview || !!importPreview.errors.length || !input('import-confirm').checked || !!importForm?.job && importForm.job.processed < importForm.job.total
  button('import-advance').disabled ||= !importForm?.job || importForm.job.processed === importForm.job.total
  button('import-resolve').disabled = busy || !importForm?.pending
  button('imports-next').disabled = busy || pending || !canWrite() || !importsNext
  button('orders-export').disabled = busy || pending || !client
  for (const name of ['import-columns','import-preview']) button(name).disabled = busy || pending
  for (const name of ['import-file','import-confirm']) input(name).disabled = busy || pending
  ;(document.getElementById('import-csv') as HTMLTextAreaElement).disabled = busy || pending
  for (const name of ['import-id','import-total','import-status']) select(name).disabled = busy || pending
  for (const element of document.querySelectorAll<HTMLButtonElement>('[data-import]')) element.disabled = busy || pending || !canWrite()
  button('order-submit').disabled = busy || pending || !canWrite() || editing?.status !== 'draft'
  button('order-fulfil').disabled = busy || pending || !canWrite() || editing?.status !== 'submitted'
  button('workflow-resolve').disabled = busy || !workflowForm?.pending
  input('workflow-confirm').disabled = busy || pending || !canWrite()
  button('order-history-next').disabled = busy || pending || !historyOrder || !historyNext
  for (const element of document.querySelectorAll<HTMLButtonElement>('[data-order-history]')) element.disabled = busy || pending
  input('token').disabled = busy
  input('url').disabled = busy || pending
  input('tenant').disabled = busy || pending
  button('connect').disabled = busy || pending
  button('reload').disabled = busy || pending || !client
  button('next').disabled = busy || pending || !next
  select('filter').disabled = busy || pending
  for (const id of ['total', 'state']) (id === 'state' ? select(id) : input(id)).disabled = id === 'state' || busy || pending || !canWrite() || !!editing && editing.status !== 'draft'
  button('save').disabled = busy || pending || !canWrite() || !!editing && editing.status !== 'draft'
  button('cancel').disabled = busy || pending || !editing
  button('resolve').disabled = busy || !form?.pending
  const owner = membership?.role === 'owner'
  for (const name of ['subscriptions-load','subscription-save','subscription-new']) button(name).disabled = busy || pending || !owner
  button('subscriptions-next').disabled = busy || pending || !owner || !subscriptionNext
  button('subscription-resolve').disabled = busy || !subscriptionForm?.pending
  button('subscription-deliveries').disabled = busy || pending || !owner || !selectedSubscription
  button('subscription-replay-resolve').disabled = busy || !subscriptionReplay?.pending
  for (const name of ['subscription-history','subscription-test']) button(name).disabled = busy || pending || !owner || !selectedSubscription
  button('subscription-test').disabled ||= !input('subscription-test-confirm').checked
  input('subscription-test-confirm').disabled = busy || pending || !owner || !selectedSubscription
  button('subscription-test-resolve').disabled = busy || !testForm?.pending
  button('subscription-deliveries-next').disabled = busy || pending || !owner || !selectedSubscription || !subscriptionDeliveryNext
  button('subscription-attempts-next').disabled = busy || pending || !owner || !selectedSubscription || !subscriptionAttemptEvent || !subscriptionAttemptNext
  button('subscription-history-next').disabled = busy || pending || !owner || !selectedSubscription || !subscriptionHistoryNext
  for (const name of ['subscription-name','subscription-config','subscription-enabled','subscription-paused']) input(name).disabled = busy || pending || !owner
  select('subscription-events').disabled = busy || pending || !owner
  for (const element of document.querySelectorAll<HTMLButtonElement>('[data-subscription]')) element.disabled = busy || pending || !owner
  for (const name of ['delivery-health','batch-preview','operations-load']) button(name).disabled = busy || pending || !owner
  button('delivery-pause').disabled = busy || pending || !owner || !queueHealth || queueHealth.paused
  button('delivery-resume').disabled = busy || pending || !owner || !queueHealth || !queueHealth.paused
  button('batch-replay').disabled = busy || pending || !owner || !batchReviewed?.length || !input('batch-confirm').checked
  input('batch-confirm').disabled = busy || pending || !owner
  button('control-resolve').disabled = busy || !controlForm?.pending
  button('operations-next').disabled = busy || pending || !owner || !operationsNext
  button('deliveries-load').disabled = busy || pending || !owner
  button('deliveries-next').disabled = busy || pending || !owner || !deliveryNext
  button('delivery-resolve').disabled = busy || !replayForm?.pending
  button('attempts-next').disabled = busy || pending || !owner || !attemptNext
  for (const element of document.querySelectorAll<HTMLButtonElement>('[data-delivery]')) element.disabled = busy || pending || !owner
  for (const name of ['invite-create','invites-load','invite-history']) button(name).disabled = busy || pending || !owner
  button('invites-next').disabled = busy || pending || !owner || !inviteNext
  button('invite-accept').disabled = busy || pending
  button('invite-resolve').disabled = busy || !inviteForm?.pending
  input('invite-link').disabled = busy || pending
  select('invite-role').disabled = busy || pending || !owner
  for (const element of document.querySelectorAll<HTMLButtonElement>('[data-invitation]')) element.disabled = busy || pending || !owner
  for (const name of ['members-load', 'audit-load', 'member-save']) button(name).disabled = busy || pending || !owner
  button('members-next').disabled = busy || pending || !owner || !memberNext
  button('audit-next').disabled = busy || pending || !owner || !auditNext
  button('member-new').disabled = busy || pending || !owner
  for (const name of ['member-remove', 'ownership-transfer']) button(name).disabled = busy || pending || !owner || !editingMember || (name === 'ownership-transfer' && editingMember.subject === membership?.subject)
  button('member-resolve').disabled = busy || !memberForm?.pending
  for (const name of ['member-subject', 'member-confirm']) input(name).disabled = busy || pending || !owner || (name === 'member-subject' && Boolean(editingMember))
  select('member-role').disabled = busy || pending || !owner
  for (const element of document.querySelectorAll<HTMLButtonElement>('[data-member]')) element.disabled = busy || pending || !owner
  for (const element of document.querySelectorAll<HTMLButtonElement>('[data-edit]')) element.disabled = busy || pending || !canWrite()
}
async function action(task: () => Promise<void>) {
  if (busy) return
  busy = true; actionToken = input('token').value; input('token').value = ''; controls()
  try { if (!actionToken) throw new Error('Supply a current application token.'); await task() }
  catch (error) { text('message', error instanceof Error && ['Supply a current application token.', 'Could not load orders. Check your session and try again.', 'Could not verify team access. Check your session and try again.'].includes(error.message) ? error.message : 'The request failed. Check your session and team access.') }
  finally { actionToken = ''; busy = false; controls() }
}
function edit(order: Order | null) {
  input('workflow-confirm').checked = false
  editing = order; input('total').value = String(order?.total_cents ?? 0); select('state').value = order?.status ?? 'draft'
  text('form-title', order ? `Edit order ${order.id}` : 'New order'); controls()
}
async function load(after?: OrderCursor) {
  clearBulkReview()
  const result = await orderOperations(client!).orders(currentCriteria(), after)
  const rows = document.getElementById('rows')!; rows.replaceChildren()
  for (const order of result.rows) {
    const row = document.createElement('tr')
    for (const value of [order.id, order.total_cents, order.status, order.version]) { const cell = document.createElement('td'); cell.textContent = String(value); row.append(cell) }
    const cell = document.createElement('td'), mark = document.createElement('input'); mark.type = 'checkbox'; mark.style.width = 'auto'; mark.dataset.bulkOrder = order.id; mark.dataset.status = order.status; mark.setAttribute('aria-label',`Select order ${order.id} for bulk fulfilment`); mark.onchange = () => { if (mark.checked) bulkSelected.set(order.id,order); else bulkSelected.delete(order.id); invalidateBulkPreview(); controls() }; cell.append(mark); const editButton = document.createElement('button')
    editButton.textContent = 'Edit'; editButton.dataset.edit = order.id; editButton.onclick = () => { if (!busy && !form?.pending && !memberForm?.pending && !inviteForm?.pending && !importForm?.pending && !workflowForm?.pending && !replayForm?.pending && !controlForm?.pending && !subscriptionForm?.pending && !subscriptionReplay?.pending && !testForm?.pending && !viewForm?.pending && !bulkForm?.pending && !sharedForm?.pending) edit(order) }; cell.append(editButton); const historyButton = document.createElement('button'); historyButton.textContent = 'History'; historyButton.dataset.orderHistory = order.id; historyButton.onclick = () => { if (!busy && !form?.pending && !memberForm?.pending && !inviteForm?.pending && !importForm?.pending && !workflowForm?.pending && !replayForm?.pending && !controlForm?.pending && !subscriptionForm?.pending && !subscriptionReplay?.pending && !testForm?.pending && !viewForm?.pending && !bulkForm?.pending && !sharedForm?.pending) void action(() => loadOrderHistory(order.id)) }; cell.append(historyButton); row.append(cell); rows.append(row)
  }
  next = result.next; text('message', `${result.rows.length} orders loaded.`)
}
function resetSession() {
  if (busy || form?.pending || memberForm?.pending || inviteForm?.pending || importForm?.pending || workflowForm?.pending || replayForm?.pending || controlForm?.pending || subscriptionForm?.pending || subscriptionReplay?.pending || testForm?.pending || viewForm?.pending || bulkForm?.pending || sharedForm?.pending) return
  client = null; form = null; membership = null; next = null; administration = null; memberForm = null; inviteForm = null; importForm = null; workflowForm = null; replayForm = null; controlForm = null; subscriptionForm = null; subscriptionReplay = null; testForm = null; viewForm = null; bulkForm = null; sharedForm = null; clearOrderOperations(); clearSubscriptions(); clearControlState(); clearDeliveries(); clearOrderHistory(); clearImports(); clearInvitations(); clearAdministration(); edit(null)
  document.getElementById('rows')!.replaceChildren(); text('role', 'Connect to see your team role.'); controls()
}
input('url').oninput = resetSession
input('tenant').oninput = resetSession
button('connect').onclick = () => void action(async () => {
  if (form?.pending || memberForm?.pending || inviteForm?.pending || importForm?.pending || workflowForm?.pending || replayForm?.pending || controlForm?.pending || subscriptionForm?.pending || subscriptionReplay?.pending || testForm?.pending || viewForm?.pending || bulkForm?.pending || sharedForm?.pending) return
  client = null; form = null; membership = null; next = null; editing = null; administration = null; memberForm = null; inviteForm = null; importForm = null; workflowForm = null; replayForm = null; controlForm = null; subscriptionForm = null; subscriptionReplay = null; testForm = null; viewForm = null; bulkForm = null; sharedForm = null; clearOrderOperations(); clearSubscriptions(); clearControlState(); clearDeliveries(); clearOrderHistory(); clearImports(); clearInvitations(); clearAdministration()
  document.getElementById('rows')!.replaceChildren(); text('role', 'Checking team access…')
  const candidate = teamsClient({ url: input('url').value, accessToken: () => actionToken, onResponse(info) { text('diagnostics', `HTTP ${info.status}${info.requestId ? ` · Request ${info.requestId}` : ''}`) } }, input('tenant').value)
  const member = await candidate.membership()
  if (!member) { text('message', 'Your session has no membership in this team.'); return }
  client = candidate; membership = member; form = createOrderForm(client); administration = teamAdministration(client); memberForm = createMembershipForm(client); importForm = createImportForm(client); workflowForm = createWorkflowForm(client); replayForm = createReplayForm(client); controlForm = createDeliveryControlForm(client); subscriptionForm = createSubscriptionForm(client); testForm = createWebhookTestForm(client); viewForm = createOrderViewForm(client); bulkForm = createBulkFulfilmentForm(client); sharedForm = createSharedViewForm(client); edit(null)
  showMembership(member)
  await load()
})
button('reload').onclick = () => void action(async () => { edit(null); await load() })
button('next').onclick = () => void action(async () => { edit(null); await load(next ?? undefined) })
function filtersChanged() { next = null; clearBulkReview(); text('message', 'Choose Reload to apply these filters and sorting.'); controls() }
select('filter').onchange = filtersChanged
select('view-sort').onchange = filtersChanged
input('view-min').oninput = filtersChanged
input('view-max').oninput = filtersChanged
button('cancel').onclick = () => edit(null)
async function save(recovering: boolean) {
  const result = recovering ? await form!.reconcile() : await form!.submit({ total: input('total').value, status: select('state').value, ...(editing ? {id: editing.id, version: editing.version} : {}) })
  if (result.outcome === 'saved') {
    edit(null)
    // Display the confirmed result before a separate read: a failed refresh
    // must never make a committed save appear uncertain or replay it.
    text('message', `Saved order ${result.order.id} at version ${result.order.version}. Choose Reload to refresh the list.`)
  } else if (result.outcome === 'failed' || result.outcome === 'invalid') text('message', result.message)
}
button('save').onclick = () => void action(() => save(false))
button('resolve').onclick = () => void action(() => save(true))
function showMembership(member: Membership | null) {
  membership = member
  text('role', member ? `Team ${member.tenant_id} · ${member.role}${member.role === 'member' ? ' (read only)' : ''}` : 'Your session has no membership in this team.')
  text('admin-access', member?.role === 'owner' ? 'Owners can add verified users, change access and transfer ownership.' : 'Only owners can manage membership and view audit history.')
  if (member?.role !== 'owner') { clearAdministration(); clearInvitations(); clearDeliveries(); clearControlState(); clearSubscriptions() }
  if (member?.role === 'member') clearBulkReview()
  if (!member) { clearOrderOperations(); document.getElementById('rows')!.replaceChildren(); next = null; edit(null) }
}
function editMember(member: ManagedMember | null) {
  editingMember = member; input('member-subject').value = member?.subject ?? ''; select('member-role').value = member?.role ?? 'member'; input('member-confirm').checked = false
  text('member-title', member ? `Manage ${member.subject}` : 'Add existing user'); button('member-save').textContent = member ? 'Change role' : 'Add user'; controls()
}
function clearAdministration() {
  memberNext = null; auditNext = null; editingMember = null
  document.getElementById('members')!.replaceChildren(); document.getElementById('audit')!.replaceChildren()
  input('member-subject').value = ''; select('member-role').value = 'member'; input('member-confirm').checked = false
  text('member-title', 'Add existing user'); button('member-save').textContent = 'Add user'
}
async function adminAction(task: () => Promise<void>) {
  await action(async () => { try { await task() } catch { text('admin-message', 'Could not load team administration. Only current owners have access; reconnect and check your role.') } })
}
async function loadMembers(after = '') {
  const page = await administration!.members(after), tbody = document.getElementById('members')!; tbody.replaceChildren()
  for (const member of page.rows) {
    const tr = document.createElement('tr')
    for (const value of [member.subject, member.role]) { const cell = document.createElement('td'); cell.textContent = value; tr.append(cell) }
    const cell = document.createElement('td'), manage = document.createElement('button'); manage.textContent = 'Manage'; manage.dataset.member = member.subject
    manage.onclick = () => { if (!busy && !memberForm?.pending && !form?.pending && !inviteForm?.pending && !importForm?.pending && !workflowForm?.pending && !replayForm?.pending && !controlForm?.pending && !subscriptionForm?.pending && !subscriptionReplay?.pending && !testForm?.pending && !viewForm?.pending && !bulkForm?.pending && !sharedForm?.pending) editMember(member) }; cell.append(manage); tr.append(cell); tbody.append(tr)
  }
  memberNext = page.next; editMember(null); text('admin-message', `${page.rows.length} members loaded.`)
}
async function loadAudit(after?: AuditCursor) {
  const page = await administration!.history(after), tbody = document.getElementById('audit')!; tbody.replaceChildren()
  for (const event of page.rows) {
    const tr = document.createElement('tr')
    const transition = `${event.action}: ${event.before_role ?? 'no membership'} → ${event.after_role ?? 'removed'}${event.action === 'transfer' ? '; actor → admin' : ''}`
    for (const value of [new Date(event.occurred_at).toLocaleString(), event.actor, event.target_subject, transition]) { const cell = document.createElement('td'); cell.textContent = value; tr.append(cell) }
    tbody.append(tr)
  }
  auditNext = page.next; text('admin-message', `${page.rows.length} changes loaded.`)
}
async function changeMembership(kind: 'change' | 'remove' | 'transfer' | 'recover') {
  if (kind !== 'recover' && !input('member-confirm').checked) { text('admin-message', 'Confirm this access change before submitting.'); return }
  const result = kind === 'recover' ? await memberForm!.reconcile() : await memberForm!.submit({ kind: kind === 'transfer' ? 'transfer' : 'change', target: input('member-subject').value, revision: editingMember?.revision ?? null, role: kind === 'remove' ? null : kind === 'transfer' ? 'owner' : select('member-role').value as TeamRole })
  if (result.outcome === 'changed') {
    // Confirm before refreshing permissions: a failed read must not turn an
    // already committed operation into another write or a pending operation.
    text('admin-message', `Access change confirmed: ${result.event.action}. Reload members and history to see current state.`)
    editMember(null)
    try { showMembership(await client!.membership()) } catch { membership = null; clearAdministration(); text('admin-access', 'Change confirmed. Reconnect to verify current access.') }
  } else if (result.outcome === 'failed' || result.outcome === 'invalid') text('admin-message', result.message)
}
button('members-load').onclick = () => void adminAction(() => loadMembers())
button('members-next').onclick = () => void adminAction(() => loadMembers(memberNext ?? ''))
button('audit-load').onclick = () => void adminAction(() => loadAudit())
button('audit-next').onclick = () => void adminAction(() => loadAudit(auditNext ?? undefined))
button('member-new').onclick = () => editMember(null)
button('member-save').onclick = () => void adminAction(() => changeMembership('change'))
button('member-remove').onclick = () => void adminAction(() => changeMembership('remove'))
button('ownership-transfer').onclick = () => void adminAction(() => changeMembership('transfer'))
button('member-resolve').onclick = () => void adminAction(() => changeMembership('recover'))

function clearInvitations() {
  inviteNext = null; input('invite-share').value = ''
  document.getElementById('invites')!.replaceChildren(); document.getElementById('invitation-history')!.replaceChildren()
}
function invitationClient() {
  return teamsClient({ url: input('url').value, accessToken: () => actionToken }, input('tenant').value)
}
async function invitationAction(value?: unknown, recovering = false) {
  // Acceptance does not require an existing membership or a successful Connect.
  inviteForm ??= createInvitationForm(invitationClient())
  const result = recovering ? await inviteForm.reconcile() : await inviteForm.submit(value)
  if (result.outcome === 'confirmed') {
    if (result.secret) input('invite-share').value = invitationLink(location.href, result.invitation.tenant_id, result.invitation.invitation_id, result.secret)
    input('invite-link').value = ''
    text('invite-message', result.secret ? 'Invitation created. Share the link privately; clearing it or reloading this page discards the secret.' : 'Invitation operation confirmed. Connect or reload to see current access.')
  } else if (result.outcome === 'failed') text('invite-message', result.message)
}
button('invite-create').onclick = () => void action(() => invitationAction({ kind: 'create', role: select('invite-role').value }))
button('invite-accept').onclick = () => {
  const link = input('invite-link').value; input('invite-link').value = ''
  void action(() => invitationAction({ kind: 'accept', link }))
}
button('invite-resolve').onclick = () => void action(() => invitationAction(undefined, true))
button('invite-clear').onclick = () => { input('invite-share').value = '' }
async function loadInvitations(after?: string) {
  try {
    const page = await teamInvitations(invitationClient()).list(after), tbody = document.getElementById('invites')!; tbody.replaceChildren()
    for (const invite of page.rows) {
      const tr = document.createElement('tr')
      for (const value of [invite.invited_role, new Date(invite.expires_at).toLocaleString(), invite.accepted_at ? `Accepted by ${invite.accepted_subject}` : invite.revoked_at ? 'Revoked' : Date.parse(invite.expires_at) <= Date.now() ? 'Expired' : 'Pending']) { const td = document.createElement('td'); td.textContent = value; tr.append(td) }
      const td = document.createElement('td')
      if (!invite.accepted_at && !invite.revoked_at) { const revoke = document.createElement('button'); revoke.textContent = 'Revoke'; revoke.dataset.invitation = invite.invitation_id; revoke.onclick = () => { if (!busy && !inviteForm?.pending && !form?.pending && !memberForm?.pending && !importForm?.pending && !workflowForm?.pending && !replayForm?.pending && !controlForm?.pending && !subscriptionForm?.pending && !subscriptionReplay?.pending && !testForm?.pending && !viewForm?.pending && !bulkForm?.pending && !sharedForm?.pending) void action(() => invitationAction({ kind: 'revoke', id: invite.invitation_id })) }; td.append(revoke) }
      tr.append(td); tbody.append(tr)
    }
    inviteNext = page.next; text('invite-message', `${page.rows.length} invitations loaded.`)
  } catch { text('invite-message', 'Only current owners can load invitations. Check your session and team access.') }
}
button('invites-load').onclick = () => void action(() => loadInvitations())
button('invites-next').onclick = () => void action(() => loadInvitations(inviteNext ?? undefined))
button('invite-history').onclick = () => void action(async () => {
  try { const events = await teamInvitations(invitationClient()).history(), list = document.getElementById('invitation-history')!; list.replaceChildren(); for (const event of events) { const li = document.createElement('li'); li.textContent = `${new Date(event.occurred_at).toLocaleString()} · ${event.actor} · ${event.action} · ${event.invitation_id}`; list.append(li) } }
  catch { text('invite-message', 'Only current owners can load invitation history.') }
})
// Consume the fragment immediately. It is never sent to the browser server,
// stored in browser storage, or used to choose an API endpoint.
if (location.hash) {
  const link = location.href; history.replaceState(null, '', location.pathname + location.search)
  try { const invitation = readInvitationLink(link); input('tenant').value = invitation.team; input('invite-link').value = link; text('invite-message', 'Choose your trusted API URL and supply a signed-in application token to accept.') }
  catch { text('invite-message', 'This invitation link is invalid.') }
}
function clearImports() {
  importPreview = null; importsNext = null; input('import-confirm').checked = false
  document.getElementById('imports')!.replaceChildren(); document.getElementById('import-preview-rows')!.replaceChildren(); document.getElementById('import-errors')!.replaceChildren()
  text('import-preview-message', 'Review the CSV for this team before importing.'); text('import-message', '')
}
function invalidatePreview() { importPreview = null; input('import-confirm').checked = false; document.getElementById('import-preview-rows')!.replaceChildren(); document.getElementById('import-errors')!.replaceChildren(); text('import-preview-message', 'CSV or mapping changed. Validate again.'); controls() }
const csvInput = () => document.getElementById('import-csv') as HTMLTextAreaElement
function readColumns() {
  invalidatePreview()
  const headers = parseCsv(csvInput().value)[0]
  for (const name of ['import-id','import-total','import-status']) {
    const target = select(name); target.replaceChildren()
    if (name === 'import-id') { const option = document.createElement('option'); option.value = ''; option.textContent = 'Generate IDs'; target.append(option) }
    for (const header of headers) { const option = document.createElement('option'); option.value = header; option.textContent = header; target.append(option) }
    const preferred = name === 'import-id' ? 'id' : name === 'import-total' ? 'total_cents' : 'status'
    target.value = headers.includes(preferred) ? preferred : name === 'import-id' ? '' : headers[name === 'import-status' ? 1 : 0] ?? ''
  }
}
button('import-columns').onclick = () => { try { readColumns() } catch (e) { text('import-preview-message', e instanceof Error ? e.message : 'Invalid CSV.') } }
csvInput().oninput = () => { importSource = 'pasted-orders.csv'; invalidatePreview() }
for (const name of ['import-id','import-total','import-status']) select(name).onchange = invalidatePreview
input('import-confirm').onchange = controls
input('import-file').onchange = () => void (async () => {
  if (busy || form?.pending || memberForm?.pending || inviteForm?.pending || importForm?.pending || workflowForm?.pending || replayForm?.pending || controlForm?.pending || subscriptionForm?.pending || subscriptionReplay?.pending || testForm?.pending || viewForm?.pending || bulkForm?.pending || sharedForm?.pending) return
  const file = input('import-file').files?.[0]; if (!file) return
  busy = true; controls()
  try { if (file.size > 1048576) throw new Error('CSV exceeds 1 MiB.'); csvInput().value = await file.text(); importSource = file.name.slice(0,128) || 'orders.csv'; readColumns() }
  catch (e) { invalidatePreview(); text('import-preview-message', e instanceof Error ? e.message : 'Could not read CSV.') }
  finally { input('import-file').value = ''; busy = false; controls() }
})()
button('import-preview').onclick = () => {
  invalidatePreview()
  try {
    importPreview = previewCsv(csvInput().value, { id: select('import-id').value || undefined, total: select('import-total').value, status: select('import-status').value }, membership && client ? {tenant:client.tenant,subject:membership.subject} : undefined)
    text('import-preview-message', `${importPreview.total} records · ${importPreview.valid} valid · ${importPreview.invalid} invalid.${importPreview.errorsTruncated ? ' Showing the first 100 errors.' : ''} Preview shows up to 20 valid records. No database writes performed.`)
    for (const error of importPreview.errors) { const li = document.createElement('li'); li.textContent = `Record ${error.row}: ${error.message}`; document.getElementById('import-errors')!.append(li) }
    for (const row of importPreview.items.slice(0,20)) { const tr = document.createElement('tr'); for (const value of [row.id, row.total_cents, row.status]) { const td = document.createElement('td'); td.textContent = String(value); tr.append(td) } document.getElementById('import-preview-rows')!.append(tr) }
  } catch (e) { text('import-preview-message', e instanceof Error ? e.message : 'Could not preview CSV.') }
  controls()
}
async function importAction(kind: 'start' | 'advance' | 'recover' | 'resume', id?: string) {
  if (kind === 'start' && (!importPreview || importPreview.errors.length || !input('import-confirm').checked)) { text('import-message', 'Validate every record and approve the import first.'); return }
  const result = kind === 'start' ? await importForm!.start(importSource, importPreview!.items) : kind === 'advance' ? await importForm!.advance() : kind === 'resume' ? await importForm!.resume(id!) : await importForm!.reconcile()
  if (result.outcome === 'confirmed') { const job = result.job; text('import-message', `Import ${job.import_id}: ${job.processed}/${job.total} processed · ${job.inserted} inserted · ${job.skipped} skipped.${job.completed_at ? ' Complete.' : ' Continue to process the next batch.'}`) }
  else if (result.outcome === 'failed') text('import-message', result.message)
}
button('import-start').onclick = () => void action(() => importAction('start'))
button('import-advance').onclick = () => void action(() => importAction('advance'))
button('import-resolve').onclick = () => void action(() => importAction('recover'))
async function loadImports(after?: string) {
  try {
    const page = await orderImports(client!).history(after), tbody = document.getElementById('imports')!; tbody.replaceChildren()
    for (const job of page.rows) { const tr = document.createElement('tr'); for (const value of [job.source_name, job.actor, new Date(job.created_at).toLocaleString(), `${job.processed}/${job.total}`, job.inserted, job.skipped]) { const td = document.createElement('td'); td.textContent = String(value); tr.append(td) } const td = document.createElement('td'); if (job.actor === membership?.subject) { const resume = document.createElement('button'); resume.textContent = 'Resume / inspect'; resume.dataset.import = job.import_id; resume.onclick = () => { if (!busy && !form?.pending && !memberForm?.pending && !inviteForm?.pending && !importForm?.pending && !workflowForm?.pending && !replayForm?.pending && !controlForm?.pending && !subscriptionForm?.pending && !subscriptionReplay?.pending && !testForm?.pending && !viewForm?.pending && !bulkForm?.pending && !sharedForm?.pending) void action(() => importAction('resume', job.import_id)) }; td.append(resume) } tr.append(td); tbody.append(tr) }
    importsNext = page.next
  } catch { text('import-message', 'Could not load import history. Check your team access.') }
}
button('imports-load').onclick = () => void action(() => loadImports())
button('imports-next').onclick = () => void action(() => loadImports(importsNext ?? undefined))
button('orders-export').onclick = () => void action(async () => {
  const csv = await exportOrders(client!, currentCriteria()), url = URL.createObjectURL(new Blob([csv], { type: 'text/csv;charset=utf-8' }))
  const anchor = document.createElement('a'); anchor.href = url; anchor.download = 'team-orders.csv'; anchor.click(); setTimeout(() => URL.revokeObjectURL(url), 1000)
  text('message', 'Filtered CSV exported. Spreadsheet formula prefixes were escaped.')
})

function clearOrderHistory() { historyOrder = null; historyNext = null; document.getElementById('order-history')!.replaceChildren(); text('history-order','Choose History beside an order.'); text('workflow-message','') }
async function loadOrderHistory(id: string, after = 1) {
  try {
    const page = await orderWorkflow(client!).history(id, after), tbody = document.getElementById('order-history')!; tbody.replaceChildren()
    for (const event of page.rows) { const tr = document.createElement('tr'); for (const value of [event.version, new Date(event.occurred_at).toLocaleString(), event.actor, event.action, `${event.before_status} → ${event.after_status}`, event.total_cents]) { const td = document.createElement('td'); td.textContent = String(value); tr.append(td) } tbody.append(tr) }
    historyOrder = id; historyNext = page.next; text('history-order',`Order ${id}: ${page.rows.length} events. History starts with updates after the workflow migration.`)
  } catch { text('history-order','Could not load order history. Check team access.') }
}
async function orderAction(next: 'submitted' | 'fulfilled' | 'recover') {
  if (next !== 'recover' && (!editing || !input('workflow-confirm').checked)) { text('workflow-message','Choose an order and confirm its status change first.'); return }
  const result = next === 'recover' ? await workflowForm!.reconcile() : await workflowForm!.submit(editing, next)
  if (result.outcome === 'changed') { edit(result.order); text('workflow-message',`Order ${result.order.id} confirmed ${result.order.status} at version ${result.order.version}. Reload or view history for current data.`) }
  else if (result.outcome === 'failed' || result.outcome === 'invalid') text('workflow-message',result.message)
}
button('order-submit').onclick = () => void action(() => orderAction('submitted'))
button('order-fulfil').onclick = () => void action(() => orderAction('fulfilled'))
button('workflow-resolve').onclick = () => void action(() => orderAction('recover'))
button('order-history-next').onclick = () => void action(() => loadOrderHistory(historyOrder!, historyNext!))

function clearDeliveries() { deliveryNext = null; attemptEvent = null; attemptNext = null; document.getElementById('deliveries')!.replaceChildren(); document.getElementById('delivery-attempts')!.replaceChildren() }
async function loadDeliveries(after?: string) {
  try {
    const page = await webhookDeliveries(client!).list(after), tbody = document.getElementById('deliveries')!; tbody.replaceChildren()
    for (const row of page.rows) {
      const tr = document.createElement('tr'); for (const value of [`${row.order_id} / ${row.version}`, row.state === 'leased' && Date.parse(row.lease_until!) <= Date.now() ? (row.attempts === 5 ? 'Expired final lease' : 'Expired lease') : row.state, `${row.attempts}/5 (lifetime ${row.total_attempts})`, row.generation]) { const td = document.createElement('td'); td.textContent = String(value); tr.append(td) }
      const td = document.createElement('td'), inspect = document.createElement('button'); inspect.textContent = 'Attempts'; inspect.dataset.delivery = row.event_id; inspect.onclick = () => { if (!busy && !replayForm?.pending && !controlForm?.pending && !subscriptionForm?.pending && !subscriptionReplay?.pending && !testForm?.pending && !viewForm?.pending && !bulkForm?.pending && !sharedForm?.pending) void action(() => loadAttempts(row.event_id)) }; td.append(inspect)
      if (row.state === 'dead' || row.state === 'leased' && row.attempts === 5 && Date.parse(row.lease_until!) <= Date.now()) { const replay = document.createElement('button'); replay.textContent = 'Replay failed delivery'; replay.dataset.delivery = row.event_id; replay.onclick = () => { if (!busy && !replayForm?.pending && !controlForm?.pending && !subscriptionForm?.pending && !subscriptionReplay?.pending && !testForm?.pending && !viewForm?.pending && !bulkForm?.pending && !sharedForm?.pending) void action(async () => { const result = await replayForm!.submit(row); text('delivery-message', result.outcome === 'replayed' ? 'Replay generation confirmed. Reload deliveries to see current state.' : result.outcome === 'failed' ? result.message : 'Replay already pending.') }) }; td.append(replay) }
      tr.append(td); tbody.append(tr)
    }
    deliveryNext = page.next; text('delivery-message', `${page.rows.length} deliveries loaded. Delivery is at least once; receivers must deduplicate event IDs.`)
  } catch { text('delivery-message','Only current team owners can inspect webhook deliveries.') }
}
async function loadAttempts(id: string, after = 0) {
  try { const page = await webhookDeliveries(client!).attempts(id, after), tbody = document.getElementById('delivery-attempts')!; tbody.replaceChildren(); for (const row of page.rows) { const tr = document.createElement('tr'); for (const value of [row.attempt, new Date(row.started_at).toLocaleString(), row.outcome, row.http_status ?? 'not recorded']) { const td = document.createElement('td'); td.textContent = String(value); tr.append(td) } tbody.append(tr) } attemptEvent = id; attemptNext = page.next }
  catch { text('delivery-message','Could not load delivery attempts. Check team access.') }
}
button('deliveries-load').onclick = () => void action(() => loadDeliveries())
button('deliveries-next').onclick = () => void action(() => loadDeliveries(deliveryNext ?? undefined))
button('attempts-next').onclick = () => void action(() => loadAttempts(attemptEvent!, attemptNext!))
button('delivery-resolve').onclick = () => void action(async () => { const result = await replayForm!.reconcile(); text('delivery-message', result.outcome === 'replayed' ? 'Replay generation confirmed. Reload deliveries to see current state.' : result.outcome === 'failed' ? result.message : 'No replay is pending.') })

function clearControlState() { queueHealth = null; batchReviewed = null; operationsNext = null; input('batch-confirm').checked = false; document.getElementById('batch-reviewed')!.replaceChildren(); document.getElementById('delivery-operations')!.replaceChildren(); text('queue-health',"Load health to inspect this team's queue.") }
async function loadHealth() {
  try { queueHealth = await deliveryControls(client!).health(); const h = queueHealth; text('queue-health', `${h.paused ? 'Paused' : 'Running'} · pending ${h.pending} · leased ${h.leased} · delivered ${h.delivered} · dead ${h.dead} · oldest pending ${h.oldest_pending_seconds === null ? 'none' : `${h.oldest_pending_seconds}s`}. Pausing does not cancel active deliveries.`) }
  catch { queueHealth = null; text('queue-health','Queue health requires current owner access.') }
}
async function controlAction(kind: 'pause' | 'resume' | 'batch' | 'recover') {
  if (kind === 'batch' && (!batchReviewed?.length || !input('batch-confirm').checked)) { text('control-message','Review the eligible failures and approve the batch first.'); return }
  if ((kind === 'pause' || kind === 'resume') && !queueHealth) return
  const result = kind === 'recover' ? await controlForm!.reconcile() : kind === 'batch' ? await controlForm!.replay(batchReviewed) : await controlForm!.pause(kind === 'pause', queueHealth!.revision)
  if (result.outcome === 'confirmed') { text('control-message', `${result.operation.action} operation confirmed. Reload deliveries and operations for current data.`); batchReviewed = null; input('batch-confirm').checked = false; document.getElementById('batch-reviewed')!.replaceChildren(); await loadHealth() }
  else if (result.outcome === 'failed') { text('control-message',result.message); if (!result.uncertain) { queueHealth = null; batchReviewed = null; input('batch-confirm').checked = false; document.getElementById('batch-reviewed')!.replaceChildren() } }
}
button('delivery-health').onclick = () => void action(loadHealth)
button('delivery-pause').onclick = () => void action(() => controlAction('pause'))
button('delivery-resume').onclick = () => void action(() => controlAction('resume'))
input('batch-confirm').onchange = controls
button('batch-preview').onclick = () => void action(async () => {
  batchReviewed = null; input('batch-confirm').checked = false; document.getElementById('batch-reviewed')!.replaceChildren()
  try { const rows = await deliveryControls(client!).preview(); batchReviewed = Object.freeze(rows.map(row => Object.freeze({event_id:row.event_id,generation:row.generation}))); for (const row of rows) { const li = document.createElement('li'); li.textContent = `${row.event_id} · order ${row.order_id} / ${row.version} · generation ${row.generation}`; document.getElementById('batch-reviewed')!.append(li) } text('control-message', `${rows.length} eligible failures reviewed. No replay was performed; approve to reset exactly these generations.`) }
  catch { text('control-message','Could not preview failed deliveries. Check current owner access.') }
})
button('batch-replay').onclick = () => void action(() => controlAction('batch'))
button('control-resolve').onclick = () => void action(() => controlAction('recover'))
async function loadOperations(after?: string) {
  try { const page = await deliveryControls(client!).history(after), list = document.getElementById('delivery-operations')!; list.replaceChildren(); for (const op of page.rows) { const li = document.createElement('li'); li.textContent = `${new Date(op.occurred_at).toLocaleString()} · ${op.actor} · ${op.action} · ${op.operation_id}`; list.append(li) } operationsNext = page.next }
  catch { text('control-message','Could not load operation history. Check current owner access.') }
}
button('operations-load').onclick = () => void action(() => loadOperations())
button('operations-next').onclick = () => void action(() => loadOperations(operationsNext ?? undefined))

function clearSubscriptionDiagnostics() { subscriptionDeliveryNext = null; subscriptionAttemptEvent = null; subscriptionAttemptNext = null; subscriptionHistoryNext = null; input('subscription-test-confirm').checked = false; for (const id of ['subscription-attempt-list','subscription-history-list']) document.getElementById(id)!.replaceChildren(); text('subscription-attempt-message',''); text('subscription-test-message','') }
function clearSubscriptions() { clearSubscriptionDiagnostics(); selectedSubscription = null; subscriptionNext = null; document.getElementById('subscriptions')!.replaceChildren(); document.getElementById('subscription-delivery-list')!.replaceChildren(); text('subscription-health','') }
function editSubscription(row: Subscription | null) { clearSubscriptionDiagnostics(); selectedSubscription = row; subscriptionReplay = row ? createReplayForm(client!,row.subscription_id) : null; input('subscription-name').value = row?.name ?? ''; input('subscription-config').value = row?.config_ref ?? ''; select('subscription-events').value = row?.event_types.length === 1 ? row.event_types[0]! : 'both'; input('subscription-enabled').checked = row?.enabled ?? true; input('subscription-paused').checked = row?.paused ?? false; document.getElementById('subscription-delivery-list')!.replaceChildren(); controls() }
async function loadSubscriptions(after?: string) {
  try { const page = await webhookSubscriptions(client!).list(after), list = document.getElementById('subscriptions')!; list.replaceChildren(); for (const row of page.rows) { const li = document.createElement('li'); li.textContent = `${row.name} · ${row.event_types.join(', ')} · config ${row.config_ref} / v${row.version} · ${row.enabled ? 'enabled' : 'disabled'} · ${row.paused ? 'paused' : 'running'}`; const manage = document.createElement('button'); manage.dataset.subscription = row.subscription_id; manage.textContent = 'Manage / health'; manage.onclick = () => { if (!busy && !subscriptionForm?.pending && !subscriptionReplay?.pending && !testForm?.pending && !viewForm?.pending && !bulkForm?.pending && !sharedForm?.pending) { editSubscription(row); void action(async () => { try { const h = await webhookSubscriptions(client!).health(row.subscription_id); text('subscription-health',`Pending ${h.pending} · leased ${h.leased} · delivered ${h.delivered} · dead ${h.dead}`) } catch { text('subscription-health','Could not load subscription health.') } }) } }; li.append(manage); list.append(li) } subscriptionNext = page.next }
  catch { text('subscription-message','Only current owners can manage subscriptions.') }
}
async function saveSubscription(recovering: boolean) {
  const result = recovering ? await subscriptionForm!.reconcile() : await subscriptionForm!.submit({...(selectedSubscription ? {id:selectedSubscription.subscription_id} : {}),revision:selectedSubscription?.revision ?? null,name:input('subscription-name').value,config_ref:input('subscription-config').value,filters:select('subscription-events').value === 'both' ? ['order.submitted','order.fulfilled'] : [select('subscription-events').value],enabled:input('subscription-enabled').checked,paused:input('subscription-paused').checked})
  if (result.outcome === 'saved') { editSubscription(result.subscription); text('subscription-message',`Subscription saved at version ${result.subscription.version}. Existing deliveries retain their pinned configuration; reload metadata to see current state.`) }
  else if (result.outcome === 'failed') text('subscription-message',result.message)
}
button('subscriptions-load').onclick = () => void action(() => loadSubscriptions())
button('subscriptions-next').onclick = () => void action(() => loadSubscriptions(subscriptionNext ?? undefined))
button('subscription-new').onclick = () => editSubscription(null)
button('subscription-save').onclick = () => void action(() => saveSubscription(false))
button('subscription-resolve').onclick = () => void action(() => saveSubscription(true))
async function loadSubscriptionDeliveries(after?: string) {
  try {
    const page = await webhookDiagnostics(client!).deliveries(selectedSubscription!.subscription_id,after), list = document.getElementById('subscription-delivery-list')!; list.replaceChildren(); subscriptionDeliveryNext = page.next
    for (const row of page.rows) {
      const li = document.createElement('li'); li.textContent = `${row.is_test ? 'Sample test' : 'Order event'} · ${row.event_id} · ${row.state} · attempts ${row.attempts} · pinned config ${row.config_ref} / v${row.subscription_version}${row.state === 'pending' ? ` · next eligible ${new Date(row.available_at).toLocaleString()}` : ''}`
      const attempts = document.createElement('button'); attempts.dataset.subscription = row.subscription_id; attempts.dataset.subscriptionAttempts = row.event_id; attempts.textContent = 'Attempt history'; attempts.onclick = () => void action(async () => { subscriptionAttemptEvent = row.event_id; await loadSubscriptionAttempts() }); li.append(attempts)
      if (row.state === 'dead' || row.state === 'leased' && row.attempts === 5 && Date.parse(row.lease_until!) <= Date.now()) {
        const replay = document.createElement('button'); replay.dataset.subscription = row.subscription_id; replay.textContent = 'Replay failed delivery'; replay.onclick = () => void action(async () => { const result = await subscriptionReplay!.submit(row); text('subscription-message',result.outcome === 'replayed' ? 'Selected subscription replay confirmed.' : result.outcome === 'failed' ? result.message : 'Replay pending.') }); li.append(replay)
      }
      list.append(li)
    }
  } catch { text('subscription-message','Could not load selected deliveries.') }
}
async function loadSubscriptionAttempts(after=0) {
  try {
    const page = await webhookDiagnostics(client!).attempts(selectedSubscription!.subscription_id,subscriptionAttemptEvent!,after), list = document.getElementById('subscription-attempt-list')!; list.replaceChildren(); subscriptionAttemptNext = page.next
    for (const row of page.rows) { const li = document.createElement('li'); const reason = row.failure_reason === 'configuration_missing' ? 'Configuration unavailable. Ask the operator to restore the pinned binding.' : row.failure_reason === 'http_error' ? 'Receiver returned an unsuccessful HTTP response.' : row.failure_reason === 'network_error' ? 'Could not reach the receiver or the request timed out.' : ''; li.textContent = `Attempt ${row.attempt} · ${new Date(row.started_at).toLocaleString()} · ${row.outcome} · HTTP ${row.http_status ?? 'none'}${row.finished_at ? ` · finished ${new Date(row.finished_at).toLocaleString()}` : ''}${reason ? ` · ${reason}` : ''}`; list.append(li) }
    text('subscription-attempt-message',page.rows.length ? `Delivery ${subscriptionAttemptEvent}. HTTP acceptance reflects this attempt's pinned destination; it does not verify a later configuration edit.` : 'No worker attempts yet. Check worker scheduling and pause controls.')
  } catch { text('subscription-attempt-message','Could not load selected attempts. Check current owner access.') }
}
async function loadSubscriptionHistory(after?: string) {
  try { const page = await webhookDiagnostics(client!).history(selectedSubscription!.subscription_id,after), list = document.getElementById('subscription-history-list')!; list.replaceChildren(); subscriptionHistoryNext = page.next; for (const row of page.rows) { const li = document.createElement('li'); const s = row.snapshot; li.textContent = `${new Date(row.receipt.occurred_at).toLocaleString()} · ${row.receipt.actor} · ${row.receipt.action} · v${s.version} · ${s.event_types.join(', ')} · ${s.config_ref} · ${s.enabled ? 'enabled' : 'disabled'} · ${s.paused ? 'paused' : 'running'}`; list.append(li) } } catch { text('subscription-message','Could not load selected change history.') }
}
async function queueSubscriptionTest(recovering: boolean) {
  const result = recovering ? await testForm!.reconcile() : await testForm!.submit(selectedSubscription,input('subscription-test-confirm').checked)
  if (result.outcome === 'queued') { input('subscription-test-confirm').checked = false; text('subscription-test-message',`Test ${result.receipt.event_id} queued for config ${result.receipt.config_ref} / v${result.receipt.subscription_version}. A worker must deliver it; pause controls still apply. Load deliveries to inspect attempts.`) }
  else if (result.outcome === 'failed') text('subscription-test-message',result.message)
}
button('subscription-deliveries').onclick = () => void action(() => loadSubscriptionDeliveries())
button('subscription-deliveries-next').onclick = () => void action(() => loadSubscriptionDeliveries(subscriptionDeliveryNext ?? undefined))
button('subscription-attempts-next').onclick = () => void action(() => loadSubscriptionAttempts(subscriptionAttemptNext ?? 0))
button('subscription-history').onclick = () => void action(() => loadSubscriptionHistory())
button('subscription-history-next').onclick = () => void action(() => loadSubscriptionHistory(subscriptionHistoryNext ?? undefined))
input('subscription-test-confirm').onchange = controls
button('subscription-test').onclick = () => void action(() => queueSubscriptionTest(false))
button('subscription-test-resolve').onclick = () => void action(() => queueSubscriptionTest(true))
button('subscription-replay-resolve').onclick = () => void action(async () => { const result = await subscriptionReplay!.reconcile(); text('subscription-message',result.outcome === 'replayed' ? 'Selected subscription replay confirmed.' : result.outcome === 'failed' ? result.message : 'No replay pending.') })

function currentCriteria(): Criteria {
  const cents = (id: string) => { const value = input(id).value; if (value === '') return null; if (!/^\d+$/.test(value)) throw new Error('Invalid range'); return Number(value) }
  return parseCriteria({status:select('filter').value || null,minimum:cents('view-min'),maximum:cents('view-max'),sort:select('view-sort').value})
}
function invalidateBulkPreview() { bulkReviewed = null; input('bulk-confirm').checked = false; document.getElementById('bulk-reviewed')!.replaceChildren(); text('bulk-message',`${bulkSelected.size} orders selected. Preview before applying.`) }
function clearBulkReview() { bulkSelected.clear(); for (const element of document.querySelectorAll<HTMLInputElement>('[data-bulk-order]')) element.checked = false; invalidateBulkPreview() }
function clearOrderOperations() { clearSharedViews(); editingView = null; viewsNext = null; bulkHistoryNext = null; clearBulkReview(); input('view-name').value = ''; input('view-min').value = ''; input('view-max').value = ''; select('view-sort').value = 'id_asc'; select('filter').value = ''; document.getElementById('saved-views')!.replaceChildren(); document.getElementById('bulk-history-list')!.replaceChildren(); text('view-message','') }
function editView(view: View | null) { clearSharedSelection(); editingView = view; input('view-name').value = view?.name ?? ''; if (view) { select('filter').value = view.status_filter ?? ''; input('view-min').value = view.minimum_total?.toString() ?? ''; input('view-max').value = view.maximum_total?.toString() ?? ''; select('view-sort').value = view.sort_order }; next = null; clearBulkReview(); controls() }
async function loadViews(after?: string) {
  try { const page = await orderOperations(client!).views(after), list = document.getElementById('saved-views')!; list.replaceChildren(); viewsNext = page.next; for (const view of page.rows) { const li = document.createElement('li'); li.textContent = `${view.name} · ${view.status_filter ?? 'all statuses'} · ${view.minimum_total ?? 'any'} to ${view.maximum_total ?? 'any'} cents · ${({id_asc:'Order ID ascending',id_desc:'Order ID descending',total_asc:'Total ascending',total_desc:'Total descending'} as Record<string,string>)[view.sort_order]}`; const apply = document.createElement('button'); apply.dataset.savedView = view.view_id; apply.textContent = 'Apply / edit view'; apply.onclick = () => void action(async () => { editView(view); await load() }); li.append(apply); list.append(li) } } catch { text('view-message','Could not load your saved views. Check current team access.') }
}
async function saveView(recovering: boolean) {
  const result = recovering ? await viewForm!.reconcile() : await viewForm!.submit({name:input('view-name').value,criteria:currentCriteria(),...(editingView ? {id:editingView.view_id} : {}),revision:editingView?.revision ?? null})
  if (result.outcome === 'saved') { editView(result.view); text('view-message',`Saved personal view ${result.view.name}. Load your views to refresh the list.`) }
  else if (result.outcome === 'failed') text('view-message',result.message)
}
async function previewBulk() {
  try { bulkReviewed = await orderOperations(client!).preview(Array.from(bulkSelected.values(),r => ({id:r.id,version:r.version}))); input('bulk-confirm').checked = false; const list = document.getElementById('bulk-reviewed')!; list.replaceChildren(); for (const row of bulkReviewed) { const li = document.createElement('li'); const reason = row.reason === 'eligible' ? 'Eligible for fulfilment' : row.reason === 'changed' ? 'Version changed. Reload and review.' : row.reason === 'not_submitted' ? 'Order is no longer submitted.' : 'Order is unavailable.'; li.textContent = `${row.id} · reviewed v${row.expected_version} · ${reason}${row.current ? ` · ${row.current.total_cents} cents · current v${row.current.version} / ${row.current.status}` : ''}`; list.append(li) }; text('bulk-message',bulkReviewed.every(r => r.reason === 'eligible') ? `${bulkReviewed.length} eligible orders. Review and approve before applying.` : 'This selection has conflicts. Nothing can be applied until you reload and preview an eligible selection.') } catch { invalidateBulkPreview(); text('bulk-message','Could not preview this selection. Check current writer access.') }
}
async function applyBulk(recovering: boolean) {
  const result = recovering ? await bulkForm!.reconcile() : await bulkForm!.submit(bulkReviewed,input('bulk-confirm').checked)
  if (result.outcome === 'fulfilled') { clearBulkReview(); text('bulk-message',`Fulfilled ${result.receipt.result.length} orders. Batch ${result.receipt.operation_id} confirmed. Reload orders to refresh the page.`) }
  else if (result.outcome === 'failed') { if (!result.uncertain) invalidateBulkPreview(); text('bulk-message',result.message) }
}
async function loadBulkHistory(after?: string) {
  try { const page = await orderOperations(client!).history(after), list = document.getElementById('bulk-history-list')!; list.replaceChildren(); bulkHistoryNext = page.next; for (const row of page.rows) { const li = document.createElement('li'); li.textContent = `${new Date(row.occurred_at).toLocaleString()} · ${row.actor} · fulfilled ${row.result.length} orders · ${row.operation_id} · ${row.result.map(order => `${order.id} / v${order.version} / ${order.total_cents} cents`).join('; ')}`; list.append(li) } } catch { text('bulk-message','Could not load batch history. Check current team access.') }
}
button('views-load').onclick = () => void action(() => loadViews())
button('views-next').onclick = () => void action(() => loadViews(viewsNext ?? undefined))
button('view-new').onclick = () => editView(null)
button('view-save').onclick = () => void action(() => saveView(false))
button('view-resolve').onclick = () => void action(() => saveView(true))
button('bulk-preview').onclick = () => void action(() => previewBulk())
input('bulk-confirm').onchange = controls
button('bulk-apply').onclick = () => void action(() => applyBulk(false))
button('bulk-resolve').onclick = () => void action(() => applyBulk(true))
button('bulk-history').onclick = () => void action(() => loadBulkHistory())
button('bulk-history-next').onclick = () => void action(() => loadBulkHistory(bulkHistoryNext ?? undefined))

function clearSharedSelection() { selectedShared = null; sharedHistoryNext = null; input('shared-view-name').value = ''; input('shared-copy-name').value = ''; document.getElementById('shared-view-history-list')!.replaceChildren(); text('shared-view-selected','Select a team view to apply its saved filters.'); button('shared-view-save').textContent = 'Publish current filters' }
function clearSharedViews() { clearSharedSelection(); sharedNext = null; document.getElementById('shared-views')!.replaceChildren(); text('shared-view-message','') }
function selectShared(view: SharedView) {
  selectedShared = view; sharedHistoryNext = null; editingView = null; input('view-name').value = ''; input('shared-view-name').value = view.name; input('shared-copy-name').value = view.name
  select('filter').value = view.status_filter ?? ''; input('view-min').value = view.minimum_total?.toString() ?? ''; input('view-max').value = view.maximum_total?.toString() ?? ''; select('view-sort').value = view.sort_order
  document.getElementById('shared-view-history-list')!.replaceChildren(); text('shared-view-selected',`Selected ${view.name} at version ${view.version}. Changes to your working filters do not update this saved view until you publish them.`); button('shared-view-save').textContent = 'Update selected team view'; next = null; clearBulkReview(); controls()
}
async function loadSharedViews(after?: string) {
  try { const page = await sharedOrderViews(client!).list(after), list = document.getElementById('shared-views')!; list.replaceChildren(); sharedNext = page.next; for (const view of page.rows) { const li = document.createElement('li'); li.textContent = `${view.name} · v${view.version} · ${view.status_filter ?? 'all statuses'} · ${view.minimum_total ?? 'any'} to ${view.maximum_total ?? 'any'} cents · changed by ${view.changed_by}`; const apply = document.createElement('button'); apply.dataset.sharedView = view.view_id; apply.textContent = 'Apply / select team view'; apply.onclick = () => void action(async () => { selectShared(view); await load() }); li.append(apply); list.append(li) } } catch { text('shared-view-message','Could not load team views. Check current team access.') }
}
async function changeSharedView(kind: 'save' | 'copy' | 'recover') {
  const result = kind === 'recover' ? await sharedForm!.reconcile() : kind === 'copy' ? await sharedForm!.copy(selectedShared,input('shared-copy-name').value) : await sharedForm!.save({name:input('shared-view-name').value,criteria:currentCriteria(),...(selectedShared ? {id:selectedShared.view_id} : {}),revision:selectedShared?.revision ?? null})
  if (result.outcome === 'confirmed') {
    if (result.receipt.action === 'copy') { editView(result.receipt.personal); text('shared-view-message',`Personal copy ${result.receipt.personal!.name} created from team-view version ${result.receipt.shared.version}. Load your views to refresh the list.`) }
    else { selectShared(result.receipt.shared); text('shared-view-message',`Team view ${result.receipt.shared.name} published at version ${result.receipt.shared.version}. Load team views to refresh the list.`) }
  } else if (result.outcome === 'failed') text('shared-view-message',result.message)
}
async function loadSharedHistory(after?: string) {
  try { const page = await sharedOrderViews(client!).history(selectedShared!.view_id,after), list = document.getElementById('shared-view-history-list')!; list.replaceChildren(); sharedHistoryNext = page.next; for (const row of page.rows) { const v = row.shared; const li = document.createElement('li'); li.textContent = `${new Date(row.occurred_at).toLocaleString()} · ${row.actor} · ${row.action === 'publish' ? 'published' : 'updated'} · ${v.name} / v${v.version} · ${v.status_filter ?? 'all statuses'} · ${v.minimum_total ?? 'any'} to ${v.maximum_total ?? 'any'} cents · ${({'id_asc':'Order ID ascending','id_desc':'Order ID descending','total_asc':'Total ascending','total_desc':'Total descending'} as Record<string,string>)[v.sort_order]}`; list.append(li) } } catch { text('shared-view-message','Could not load shared-view changes. Check current team access.') }
}
button('shared-views-load').onclick = () => void action(() => loadSharedViews())
button('shared-views-next').onclick = () => void action(() => loadSharedViews(sharedNext ?? undefined))
button('shared-view-save').onclick = () => void action(() => changeSharedView('save'))
button('shared-view-copy').onclick = () => void action(() => changeSharedView('copy'))
button('shared-view-resolve').onclick = () => void action(() => changeSharedView('recover'))
button('shared-view-new').onclick = () => { clearSharedSelection(); clearBulkReview(); controls() }
button('shared-view-history').onclick = () => void action(() => loadSharedHistory())
button('shared-view-history-next').onclick = () => void action(() => loadSharedHistory(sharedHistoryNext ?? undefined))

controls()
