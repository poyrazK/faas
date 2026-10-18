import { teamsClient } from '../src/orders.js'
const client = teamsClient({ url: 'https://orders.example', accessToken: 'test' }, 'shop-a')
void client.summary({ status: 'submitted', minimum: 100 }).then(result => {
  const count: string | null | undefined = result.data?.[0].order_count
  const cents: string | null | undefined = result.data?.[0].total_cents
  void count; void cents
  // @ts-expect-error aggregate cents remain strings rather than imprecise numbers
  const numeric: number = result.data![0].total_cents!
  void numeric
})
// @ts-expect-error summary filters use the database order-status enum
void client.summary({ status: 'paid' })
// @ts-expect-error summary inputs are filters, with no page limit
void client.summary({ pageSize: 20 })
const args = { request_key: 'key', team_id: 'team', order_id: 'id', expected_version: 0, order_total: 1, order_state: 'draft' as const }
void client.db.rpc('save_team_order_once', args).then(result => {
  const total: number | undefined = result.data?.[0].total_cents
  const status: 'draft' | 'submitted' | 'fulfilled' | undefined = result.data?.[0].status
  void total; void status
  // @ts-expect-error generated row has no customer password field
  void result.data?.[0].password
})
// @ts-expect-error enum arguments retain the database's allowed statuses
void client.db.rpc('save_team_order_once', { ...args, order_state: 'paid' })
// @ts-expect-error required RPC parameters cannot be omitted
void client.db.rpc('save_team_order_once', { request_key: 'key' })
// @ts-expect-error unapproved functions are not part of the API contract
void client.db.rpc('change_membership', {})

void client.db.rpc('change_team_member_once', {
  request_key: 'key', team_id: 'team', target_subject: 'user', expected_revision: null, new_role: 'member'
}).then(result => {
  const action: 'add' | 'change' | 'remove' | 'transfer' | undefined = result.data?.[0].action
  const before: 'owner' | 'admin' | 'member' | null | undefined = result.data?.[0].before_role
  void action; void before
})
// @ts-expect-error only database-declared roles are accepted
void client.db.rpc('change_team_member_once', { request_key: 'key', team_id: 'team', target_subject: 'user', expected_revision: null, new_role: 'superadmin' })
// @ts-expect-error transfer requires an expected membership revision
void client.db.rpc('transfer_team_ownership_once', { request_key: 'key', team_id: 'team', target_subject: 'user' })

void client.db.rpc('accept_team_invitation_once', { operation_id: 'id', team_id: 'team', invite_secret: 'secret' }).then(result => {
  const who: string | null | undefined = result.data?.[0].accepted_subject
  void who
})
// @ts-expect-error acceptance requires the bearer secret
void client.db.rpc('accept_team_invitation_once', { operation_id: 'id', team_id: 'team' })
// @ts-expect-error create requires an explicit expiration
void client.db.rpc('create_team_invitation_once', { operation_id: 'id', team_id: 'team', invite_secret: 'secret', invite_role: 'member' })

void client.db.rpc('advance_team_order_import_once', { operation_id: 'id', team_id: 'team', expected_processed: 0 }).then(result => {
  const inserted: number | undefined = result.data?.[0].inserted
  void inserted
})
// @ts-expect-error a batch requires the reviewed progress cursor
void client.db.rpc('advance_team_order_import_once', { operation_id: 'id', team_id: 'team' })

void client.db.rpc('transition_team_order_once', { request_key: 'key', team_id: 'team', order_id: 'id', expected_version: 1, next_state: 'submitted' })
// @ts-expect-error status actions require a current version
void client.db.rpc('transition_team_order_once', { request_key: 'key', team_id: 'team', order_id: 'id', next_state: 'submitted' })

void client.db.rpc('replay_team_order_webhook', {team_id:'team',delivery_id:'id',expected_generation:0}).then(result => {
  const state: 'pending'|'leased'|'delivered'|'dead'|undefined = result.data?.[0].state
  void state
})
// @ts-expect-error replay requires the observed generation
void client.db.rpc('replay_team_order_webhook', {team_id:'team',delivery_id:'id'})

void client.db.rpc('set_team_delivery_pause_once', {team_id:'team',operation_id:'id',expected_revision:null,pause_delivery:true})
// @ts-expect-error settings changes require a reviewed revision (nullable initially)
void client.db.rpc('set_team_delivery_pause_once', {team_id:'team',operation_id:'id',pause_delivery:true})

void client.db.rpc('fulfil_team_orders_once',{team_id:'team',operation_id:'id',reviewed:[{id:'order',version:2}]}).then(result => {
  const actor: string | undefined = result.data?.[0].actor
  void actor
})
// @ts-expect-error a bulk mutation requires a reviewed selection
void client.db.rpc('fulfil_team_orders_once',{team_id:'team',operation_id:'id'})
void client.db.rpc('list_view_orders',{team_id:'team',status_filter:'submitted',sort_order:'total_desc'}).then(result => {
  const status: 'draft'|'submitted'|'fulfilled'|undefined = result.data?.[0].status
  void status
})
// @ts-expect-error status filters come from the generated database enum
void client.db.rpc('list_view_orders',{team_id:'team',status_filter:'paid'})

void client.db.rpc('copy_shared_order_view_once',{team_id:'team',request_id:'id',shared_view_id:'view',expected_revision:'revision',personal_view_id:'copy',personal_name:'Mine'}).then(result=>{
  const action: string|undefined = result.data?.[0].action
  void action
})
// @ts-expect-error copying requires the reviewed source revision
void client.db.rpc('copy_shared_order_view_once',{team_id:'team',request_id:'id',shared_view_id:'view',personal_view_id:'copy',personal_name:'Mine'})
