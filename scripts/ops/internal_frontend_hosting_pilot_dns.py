"""Cloudflare DNS ownership adapter and operator-supplied Access JWT reader."""
import base64
import hashlib
import json
import time
from urllib.parse import urlencode

from internal_frontend_hosting_pilot_common import HTTPS, atomic_json, hex_id, identifier, need, private_read


class DNSController:
    def __init__(self, journal, http=None):
        self.journal, self.http = journal, http or HTTPS()
        self.cfg = journal.config

    def cf(self, method, suffix, value=None):
        return self.http.cloudflare(self.cfg['dns_token_file'], method, '/zones/' + self.cfg['dns_zone_id'] + suffix, value)

    def zone(self):
        value = self.cf('GET', '')['result']
        need(value.get('name') == self.cfg['dns_zone_name'] and value.get('account', {}).get('id') == self.cfg['dns_account_id'],
             'DNS zone differs from the isolated customer-DNS account')

    def records(self, name):
        reply = self.cf('GET', '/dns_records?' + urlencode({'name': name, 'per_page': 100}))
        records = reply['result']
        info = reply.get('result_info', {})
        need(isinstance(records, list) and len(records) < 100 and info.get('total_pages', 1) <= 1,
             'DNS record inventory was not complete and bounded')
        return records

    def normalized(self, value):
        content = value.get('content', value.get('value', ''))
        if value['type'] == 'CNAME':
            content = content.rstrip('.').lower()
        elif isinstance(content, str) and content.startswith('"'):
            content = json.loads(content)
        return {'type': value['type'], 'name': value['name'].rstrip('.').lower(), 'value': content}

    def validate_records(self, records):
        need(isinstance(records, list) and 2 <= len(records) <= 18)
        normalized = []
        for row in records:
            need(isinstance(row, dict) and set(row) == {'type', 'name', 'value'} and row['type'] in ('CNAME', 'TXT'))
            name, value = row['name'], row['value']
            need(isinstance(name, str) and len(name) <= 253 and
                 (name == self.cfg['hostname'] or name.endswith('.' + self.cfg['hostname'])) and
                 all(c.isascii() and (c.isalnum() or c in '._-') for c in name) and
                 isinstance(value, str) and 0 < len(value) <= 2048 and '\r' not in value and '\n' not in value)
            normalized.append(self.normalized(row))
        need({'type': 'CNAME', 'name': self.cfg['hostname'], 'value': 'customers.' + self.cfg['hosting_domain']} in normalized)
        proof = [r for r in normalized if r['name'] == '_gregale-hosting.' + self.cfg['hostname'] and r['type'] == 'TXT']
        need(len(proof) == 1 and proof[0]['value'].startswith('gregale-hosting=') and hex_id(proof[0]['value'].split('=', 1)[1], 64))
        return normalized

    def owned(self, record, entry):
        return record.get('comment') == entry['comment'] and self.normalized(record) == entry['record'] and \
            record.get('proxied', False) is False and (entry.get('id') is None or record.get('id') == entry['id'])

    def run(self, action, request):
        identity = self.journal.identity(request)
        need(identifier(request.get('run_id')))
        identity['run_id'] = request['run_id']
        with self.journal.locked() as state:
            dns = state.get('dns')
            need(dns is None or dns['identity'] == identity, 'DNS hostname belongs to another pilot run')
            if action == 'token':
                need(dns is not None and not dns.get('cleared'))
                aud = request.get('access_aud')
                need(hex_id(aud, 64))
                token = private_read(self.cfg['domain_access_token_file'], 16384).decode().strip()
                parts = token.split('.')
                need(len(parts) == 3 and all(parts))
                claims = json.loads(base64.urlsafe_b64decode(parts[1] + '=' * (-len(parts[1]) % 4)))
                audience = claims.get('aud')
                need(audience == aud or audience == [aud])
                need(claims.get('iss') == 'https://' + self.cfg['access_team'] and
                     type(claims.get('exp')) in (int, float) and claims['exp'] > time.time() + 60 and
                     claims.get('nbf', 0) <= time.time() + 30,
                     'Access JWT must match the app audience and issuer and remain unexpired')
                # Claims are only a local sanity check. The actual edge verifies
                # the JWT signature and Access policy; this adapter never mints one.
                return {**identity, 'access_token': token}
            if action == 'clear' and dns is None:
                # No DNS write can precede the durable intent. Preserve any
                # unrelated manual records without needing provider credentials.
                return {**identity, 'cleared': True}
            self.zone()
            if action == 'describe':
                need(dns is None, 'Use a fresh hostname and directory for a new qualification')
                need(not self.records(self.cfg['hostname']) and not self.records('_gregale-hosting.' + self.cfg['hostname']),
                     'Disposable DNS hostname already has records')
                token = self.cfg['hosting_read_token_file']
                zone = self.http.cloudflare(token, 'GET', '/zones/' + self.cfg['hosting_zone_id'])['result']
                need(zone.get('name') == self.cfg['hosting_domain'] and zone.get('account', {}).get('id') == self.cfg['cf_account_id'])
                for path in ('/zones/' + self.cfg['hosting_zone_id'] + '/custom_hostnames',
                             '/accounts/' + self.cfg['cf_account_id'] + '/access/apps'):
                    result = self.http.cloudflare(token, 'GET', path + '?' + urlencode({'hostname' if 'custom_hostnames' in path else 'domain': self.cfg['hostname'], 'per_page': 1}))
                    need(result['result'] == [], 'Disposable hostname already has provider or Access resources')
                return {**identity, 'dedicated': True, 'disposable_hostname': True}
            if action == 'sync':
                records = self.validate_records(request.get('records'))
                need(identifier(request.get('site_id')) and identifier(request.get('domain_id')))
                claim = {'site_id': request['site_id'], 'domain_id': request['domain_id']}
                need(state.get('claim') in (None, claim) and state.get('site_id') in (None, request['site_id']))
                state['claim'], state['site_id'] = claim, request['site_id']
                if dns is None:
                    dns = {'identity': identity, 'site_id': request['site_id'], 'domain_id': request['domain_id'], 'records': {}, 'cleared': False}
                    state['dns'] = dns
                need(not dns['cleared'] and dns['site_id'] == request['site_id'] and dns['domain_id'] == request['domain_id'])
                for record in records:
                    key = hashlib.sha256(json.dumps(record, sort_keys=True).encode()).hexdigest()
                    entry = dns['records'].get(key)
                    if entry is None:
                        entry = {'record': record, 'comment': 'gregale-pilot:' + identity['run_id'] + ':' + key[:16], 'id': None, 'attempted': False}
                        dns['records'][key] = entry
                    found = self.records(record['name'])
                    # Other records are allowed only if already journaled by this run.
                    need(all(any(self.owned(r, old) for old in dns['records'].values()) for r in found),
                         'DNS record is pre-existing, foreign, changed, or proxied')
                    own = [r for r in found if self.owned(r, entry)]
                    need(len(own) <= 1, 'Duplicate owned DNS records require operator recovery')
                    if own:
                        need(hex_id(own[0].get('id')))
                        entry['id'] = own[0]['id']
                    else:
                        need(not entry['attempted'] and entry['id'] is None,
                             'An uncertain DNS create is absent; review it before any further create')
                        entry['attempted'] = True
                        atomic_json(self.journal.root / 'journal.json', state)
                        value = {'type': record['type'], 'name': record['name'], 'content': record['value'],
                                 'ttl': 60, 'comment': entry['comment']}
                        if record['type'] == 'CNAME':
                            value['proxied'] = False
                        reply = self.cf('POST', '/dns_records', value)['result']
                        need(self.owned(reply, entry) and hex_id(reply.get('id')), 'Created DNS record did not match its owned intent')
                        entry['id'] = reply['id']
                    atomic_json(self.journal.root / 'journal.json', state)
                return {**identity, 'applied': True}
            need(action == 'clear' and dns is not None, 'Unknown DNS controller action or missing ownership journal')
            for entry in dns['records'].values():
                found = self.records(entry['record']['name'])
                own = [r for r in found if self.owned(r, entry)]
                need(len(own) <= 1)
                if entry['id']:
                    matching = [r for r in found if r.get('id') == entry['id']]
                    need(not matching or self.owned(matching[0], entry), 'Owned DNS record changed; refuse deletion')
                elif not own and entry['attempted']:
                    need(False, 'Uncertain DNS create has no receipt; preserve the journal for operator recovery')
                if own:
                    need(hex_id(own[0].get('id')))
                    entry['id'] = own[0]['id']
                    atomic_json(self.journal.root / 'journal.json', state)
                    self.cf('DELETE', '/dns_records/' + own[0]['id'])
                entry['cleared'] = True
                atomic_json(self.journal.root / 'journal.json', state)
            dns['cleared'] = True
            return {**identity, 'cleared': True}
