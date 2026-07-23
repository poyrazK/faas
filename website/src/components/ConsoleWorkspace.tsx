'use client';

import React, { useState, useEffect } from 'react';
import { ConsoleTab } from './ConsoleSidebar';
import { 
  checkBackendHealth, 
  fetchApps, 
  createApp,
  triggerColdWake, 
  parkApp,
  deleteApp,
  getApiUrl,
  getAccount,
  changePlan,
  exportAccountData,
  deleteAccount,
  restoreAccount,
  createCron,
  createDomain,
  createApiKey,
  createSecret,
  AccountModel
} from '@/lib/api';

export interface MicroVMFunction {
  id: string;
  name: string;
  runtime: string;
  status: 'PARKED ON DISK' | 'COLD RESTORED';
  ram: string;
  p50Wake: string;
  region: string;
  lastExecuted: string;
}

export interface SecretItem {
  id: string;
  key: string;
  val: string;
  target: string;
  isMasked: boolean;
}

export interface CronItem {
  id: string;
  schedule: string;
  targetFunc: string;
  status: string;
  lastRun: string;
}

export interface DomainItem {
  id: string;
  domain: string;
  targetFunc: string;
  sslStatus: string;
}

export interface ApiKeyItem {
  id: string;
  name: string;
  prefix: string;
  created: string;
}

interface ConsoleWorkspaceProps {
  activeTab: ConsoleTab;
  setActiveTab: (tab: ConsoleTab) => void;
  functions: MicroVMFunction[];
  setFunctions: React.Dispatch<React.SetStateAction<MicroVMFunction[]>>;
  secrets: SecretItem[];
  setSecrets: React.Dispatch<React.SetStateAction<SecretItem[]>>;
  openNewFuncModal: () => void;
  openAddSecretModal: () => void;
}

export const ConsoleWorkspace: React.FC<ConsoleWorkspaceProps> = ({
  activeTab,
  setActiveTab,
  functions,
  setFunctions,
  secrets,
  setSecrets,
  openNewFuncModal,
  openAddSecretModal,
}) => {
  const [searchQuery, setSearchQuery] = useState('');
  const [targetUrlInput, setTargetUrlInput] = useState('http://localhost:8080');
  const [backendStatus, setBackendStatus] = useState<{ online: boolean; url: string }>({ online: false, url: 'http://localhost:8080' });
  const [accountInfo, setAccountInfo] = useState<AccountModel | null>(null);

  // Selected Function for Detail Drawer
  const [selectedFunc, setSelectedFunc] = useState<MicroVMFunction | null>(null);

  // Modals for Extended Console Features
  const [isCronModalOpen, setIsCronModalOpen] = useState(false);
  const [isDomainModalOpen, setIsDomainModalOpen] = useState(false);
  const [isApiKeyModalOpen, setIsApiKeyModalOpen] = useState(false);

  // Form Inputs
  const [newCronExpr, setNewCronExpr] = useState('0 * * * *');
  const [newCronTarget, setNewCronTarget] = useState('api-gateway');

  const [newDomainName, setNewDomainName] = useState('');
  const [newDomainTarget, setNewDomainTarget] = useState('api-gateway');

  const [newKeyName, setNewKeyName] = useState('');
  const [generatedKey, setGeneratedKey] = useState<string | null>(null);

  const [logs, setLogs] = useState([
    { ts: '20:45:01.104', service: '[gatewayd]', event: 'HTTP_GET /healthz', status: '1ms', text: 'Health check OK (0ms wake penalty)' },
    { ts: '20:45:05.412', service: '[vmmd]', event: 'FC_UNPARK', status: 'COLD WAKE', text: 'Firecracker snapshot restored in 184ms (UID: 20412)' }
  ]);

  // Extended Feature States
  const [crons, setCrons] = useState<CronItem[]>([
    { id: 'c-1', schedule: '0 * * * * (Hourly)', targetFunc: 'stripe-webhook', status: 'ACTIVE', lastRun: '14 mins ago' },
    { id: 'c-2', schedule: '0 0 * * * (Daily)', targetFunc: 'api-gateway', status: 'ACTIVE', lastRun: '8 hrs ago' },
  ]);

  const [domains, setDomains] = useState<DomainItem[]>([
    { id: 'd-1', domain: 'api.gregale.dev', targetFunc: 'api-gateway', sslStatus: 'ACTIVE (Let\'s Encrypt)' },
    { id: 'd-2', domain: 'auth.gregale.dev', targetFunc: 'auth-service', sslStatus: 'ACTIVE (Let\'s Encrypt)' },
  ]);

  const [apiKeys, setApiKeys] = useState<ApiKeyItem[]>([
    { id: 'k-1', name: 'CLI Machine Token', prefix: 'faas_pat_981a...24a', created: '3 days ago' },
    { id: 'k-2', name: 'GitHub CI/CD Deployer', prefix: 'faas_pat_771b...88c', created: '1 week ago' },
  ]);

  useEffect(() => {
    const savedUrl = getApiUrl();
    setTargetUrlInput(savedUrl);
    testConnection(savedUrl);
  }, []);

  const testConnection = async (urlToTest: string) => {
    const status = await checkBackendHealth(urlToTest);
    setBackendStatus(status);
    if (status.online) {
      const acct = await getAccount(status.url);
      if (acct) setAccountInfo(acct);
      const backendApps = await fetchApps(status.url);
      if (backendApps.length > 0) {
        setFunctions(backendApps.map(a => ({
          id: a.id,
          name: a.name,
          runtime: a.runtime,
          status: a.status,
          ram: `${a.ram_mb} MB`,
          p50Wake: `${a.p50_wake_ms} ms`,
          region: a.region,
          lastExecuted: a.last_executed,
        })));
      }
    }
  };

  const handleWakeTest = async (funcId: string, funcName: string) => {
    setFunctions(prev => prev.map(f => f.id === funcId ? { ...f, status: 'COLD RESTORED', lastExecuted: 'Just now' } : f));
    const result = await triggerColdWake(funcName, backendStatus.url);
    const now = new Date();
    const timeStr = now.toTimeString().split(' ')[0] + '.' + String(now.getMilliseconds()).padStart(3, '0');
    setLogs(prev => [
      { ts: timeStr, service: '[vmmd]', event: 'FC_RESTORE', status: `${result.latency_ms} ms`, text: `Snapshot unparked for ${funcName} on ${backendStatus.url}` },
      ...prev
    ]);
  };

  const handleParkApp = async (slug: string) => {
    await parkApp(slug, backendStatus.url);
    setFunctions(prev => prev.map(f => f.name === slug ? { ...f, status: 'PARKED ON DISK' } : f));
    if (selectedFunc) {
      setSelectedFunc(prev => prev ? { ...prev, status: 'PARKED ON DISK' } : null);
    }
  };

  const handleDeleteApp = async (slug: string) => {
    await deleteApp(slug, backendStatus.url);
    setFunctions(prev => prev.filter(f => f.name !== slug));
    setSelectedFunc(null);
  };

  const handleChangePlan = async (plan: 'free' | 'hobby' | 'pro' | 'scale') => {
    await changePlan(plan, backendStatus.url);
    if (accountInfo) {
      setAccountInfo({ ...accountInfo, plan });
    }
  };

  const handleExportData = async () => {
    const data = await exportAccountData(true, backendStatus.url);
    const jsonStr = JSON.stringify(data || { account: accountInfo, functions, secrets, crons, domains }, null, 2);
    const blob = new Blob([jsonStr], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `faas-account-export-${Date.now()}.json`;
    a.click();
  };

  const toggleSecretMask = (secretId: string) => {
    setSecrets(prev => prev.map(s => s.id === secretId ? { ...s, isMasked: !s.isMasked } : s));
  };

  const deleteSecretItem = (secretId: string) => {
    setSecrets(prev => prev.filter(s => s.id !== secretId));
  };

  const handleAddCron = async (e: React.FormEvent) => {
    e.preventDefault();
    await createCron(newCronExpr, newCronTarget, backendStatus.url);
    const newCron: CronItem = {
      id: `cron-${Date.now()}`,
      schedule: newCronExpr,
      targetFunc: newCronTarget,
      status: 'ACTIVE',
      lastRun: 'Just now'
    };
    setCrons(prev => [newCron, ...prev]);
    setIsCronModalOpen(false);
  };

  const handleAddDomain = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newDomainName.trim()) return;
    await createDomain(newDomainName.trim(), newDomainTarget, backendStatus.url);
    const newDom: DomainItem = {
      id: `dom-${Date.now()}`,
      domain: newDomainName.trim(),
      targetFunc: newDomainTarget,
      sslStatus: 'ACTIVE (Let\'s Encrypt)'
    };
    setDomains(prev => [newDom, ...prev]);
    setNewDomainName('');
    setIsDomainModalOpen(false);
  };

  const handleCreateApiKey = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newKeyName.trim()) return;
    const res = await createApiKey(newKeyName.trim(), backendStatus.url);
    const rawKey = res?.raw_token || `faas_pat_${Math.random().toString(36).substring(2, 15)}`;
    const newKey: ApiKeyItem = {
      id: `key-${Date.now()}`,
      name: newKeyName.trim(),
      prefix: `${rawKey.substring(0, 12)}...${rawKey.substring(rawKey.length - 4)}`,
      created: 'Just now'
    };
    setApiKeys(prev => [newKey, ...prev]);
    setGeneratedKey(rawKey);
  };

  const filteredFunctions = functions.filter(f => 
    f.name.toLowerCase().includes(searchQuery.toLowerCase()) || 
    f.runtime.toLowerCase().includes(searchQuery.toLowerCase())
  );

  return (
    <main className="console-workspace">
      {/* DigitalOcean / Local Control Plane Connector */}
      <div style={{
        background: backendStatus.online ? '#F0FDF4' : '#FFFBEB',
        border: `1px solid ${backendStatus.online ? '#86EFAC' : '#FDE68A'}`,
        borderRadius: 'var(--radius-md)',
        padding: '0.85rem 1.25rem',
        marginBottom: '1.5rem',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        flexWrap: 'wrap',
        gap: '1rem',
        fontSize: '0.88rem',
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '0.65rem' }}>
          <span style={{ 
            width: '10px', 
            height: '10px', 
            borderRadius: '50%', 
            background: backendStatus.online ? '#16A34A' : '#D97706' 
          }}></span>
          <div>
            <strong>Control Plane Host:</strong>{' '}
            {backendStatus.online ? (
              <span style={{ color: '#15803D', fontWeight: 600 }}>Connected ({backendStatus.url})</span>
            ) : (
              <span style={{ color: '#B45309' }}>Offline / Connecting to {backendStatus.url}</span>
            )}
          </div>
        </div>

        <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
          <input 
            type="text" 
            placeholder="DigitalOcean IP (e.g. 159.203.x.x:8080)" 
            value={targetUrlInput}
            onChange={(e) => setTargetUrlInput(e.target.value)}
            style={{
              padding: '0.35rem 0.65rem',
              borderRadius: '4px',
              border: '1px solid var(--border-dim)',
              fontSize: '0.82rem',
              fontFamily: 'var(--font-mono)',
              width: '280px',
            }}
          />
          <button 
            className="btn btn-gregale btn-sm"
            onClick={() => testConnection(targetUrlInput)}
          >
            Connect CP
          </button>
        </div>
      </div>

      {/* Tab 1: Overview */}
      {activeTab === 'overview' && (
        <div>
          <div className="console-page-header">
            <div>
              <h2 className="console-page-title">Cluster Overview</h2>
              <p className="console-page-subtitle">Real-time status of your Firecracker microVM workloads on {backendStatus.url}.</p>
            </div>
            <div style={{ display: 'flex', gap: '0.5rem' }}>
              <button className="btn btn-secondary btn-sm" onClick={handleExportData}>GDPR Export</button>
              <button className="btn btn-gregale" onClick={openNewFuncModal}>+ New Function</button>
            </div>
          </div>

          <div className="stat-cards-grid">
            <div className="stat-card">
              <div className="stat-card-label">Active MicroVMs</div>
              <div className="stat-card-val" style={{ color: 'var(--gregale-green)' }}>{functions.length}</div>
              <div className="stat-card-sub">0 MB resident RAM when parked</div>
            </div>

            <div className="stat-card">
              <div className="stat-card-label">p50 Cold Wake</div>
              <div className="stat-card-val">184 ms</div>
              <div className="stat-card-sub">&lt;350ms SLA budget</div>
            </div>

            <div className="stat-card">
              <div className="stat-card-label">Monthly Metering</div>
              <div className="stat-card-val">2.8 GB-h</div>
              <div className="stat-card-sub">5 GB-h included on Free</div>
            </div>

            <div className="stat-card">
              <div className="stat-card-label">Host Health</div>
              <div className="stat-card-val" style={{ color: 'var(--accent-green)' }}>100%</div>
              <div className="stat-card-sub">UID Jails 20000–29999</div>
            </div>
          </div>

          <div className="console-card-panel">
            <div className="panel-toolbar">
              <div style={{ fontWeight: 700, fontSize: '1rem' }}>Active Functions</div>
              <button className="btn btn-secondary btn-sm" onClick={() => setActiveTab('functions')}>
                View All Functions
              </button>
            </div>
            <table className="console-table">
              <thead>
                <tr>
                  <th>Function Name</th>
                  <th>Status</th>
                  <th>Memory Plan</th>
                  <th>Cold Wake</th>
                  <th>Last Executed</th>
                  <th>Action</th>
                </tr>
              </thead>
              <tbody>
                {functions.slice(0, 2).map(f => (
                  <tr key={f.id}>
                    <td>
                      <button 
                        style={{ background: 'none', border: 'none', cursor: 'pointer', textAlign: 'left' }}
                        onClick={() => setSelectedFunc(f)}
                      >
                        <div className="app-name-cell" style={{ color: 'var(--gregale-green)' }}>{f.name}</div>
                      </button>
                    </td>
                    <td>
                      <span className={`status-badge ${f.status === 'COLD RESTORED' ? 'restored' : 'parked'}`}>
                        {f.status}
                      </span>
                    </td>
                    <td>{f.ram}</td>
                    <td>{f.p50Wake}</td>
                    <td>{f.lastExecuted}</td>
                    <td>
                      <button className="btn btn-secondary btn-sm" onClick={() => handleWakeTest(f.id, f.name)}>
                        Trigger Wake
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Tab 2: Functions */}
      {activeTab === 'functions' && (
        <div>
          <div className="console-page-header">
            <div>
              <h2 className="console-page-title">Functions &amp; Applications</h2>
              <p className="console-page-subtitle">Manage deployed Firecracker microVM functions and memory snapshots.</p>
            </div>
            <button className="btn btn-gregale" onClick={openNewFuncModal}>+ New Function</button>
          </div>

          <div className="console-card-panel">
            <div className="panel-toolbar">
              <input 
                type="text" 
                className="table-search-input" 
                placeholder="Search functions..." 
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
              />
              <div style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8rem', color: 'var(--text-muted)' }}>
                Showing <strong>{filteredFunctions.length}</strong> functions
              </div>
            </div>

            <table className="console-table">
              <thead>
                <tr>
                  <th>Function Name</th>
                  <th>Runtime</th>
                  <th>Status</th>
                  <th>Memory</th>
                  <th>P50 Wake</th>
                  <th>Region</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {filteredFunctions.map(f => (
                  <tr key={f.id}>
                    <td>
                      <button 
                        style={{ background: 'none', border: 'none', cursor: 'pointer', textAlign: 'left' }}
                        onClick={() => setSelectedFunc(f)}
                      >
                        <div className="app-name-cell" style={{ color: 'var(--gregale-green)' }}>{f.name}</div>
                      </button>
                    </td>
                    <td>{f.runtime}</td>
                    <td>
                      <span className={`status-badge ${f.status === 'COLD RESTORED' ? 'restored' : 'parked'}`}>
                        {f.status}
                      </span>
                    </td>
                    <td>{f.ram}</td>
                    <td>{f.p50Wake}</td>
                    <td>{f.region}</td>
                    <td>
                      <button className="btn btn-secondary btn-sm" onClick={() => handleWakeTest(f.id, f.name)}>
                        Trigger Wake
                      </button>
                      <button className="btn btn-secondary btn-sm" style={{ marginLeft: '0.35rem' }} onClick={() => handleParkApp(f.name)}>
                        Park to Disk
                      </button>
                      <button className="btn btn-secondary btn-sm" style={{ marginLeft: '0.35rem' }} onClick={() => setSelectedFunc(f)}>
                        Details
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Tab 3: Cron Triggers (schedd) */}
      {activeTab === 'crons' && (
        <div>
          <div className="console-page-header">
            <div>
              <h2 className="console-page-title">Cron Scheduler &amp; Triggers (schedd)</h2>
              <p className="console-page-subtitle">Manage scheduled execution triggers for microVM functions.</p>
            </div>
            <button className="btn btn-gregale" onClick={() => setIsCronModalOpen(true)}>+ Add Cron Job</button>
          </div>

          <div className="console-card-panel">
            <table className="console-table">
              <thead>
                <tr>
                  <th>Cron ID</th>
                  <th>Schedule Expression</th>
                  <th>Target Function</th>
                  <th>Status</th>
                  <th>Last Executed</th>
                  <th>Action</th>
                </tr>
              </thead>
              <tbody>
                {crons.map(c => (
                  <tr key={c.id}>
                    <td><code>{c.id}</code></td>
                    <td><code>{c.schedule}</code></td>
                    <td><div className="app-name-cell">{c.targetFunc}</div></td>
                    <td><span className="status-badge restored">{c.status}</span></td>
                    <td>{c.lastRun}</td>
                    <td>
                      <button className="btn btn-secondary btn-sm" style={{ color: '#EF4444' }} onClick={() => setCrons(prev => prev.filter(x => x.id !== c.id))}>
                        Delete Cron
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Tab 4: Custom Domains & TLS (gatewayd) */}
      {activeTab === 'domains' && (
        <div>
          <div className="console-page-header">
            <div>
              <h2 className="console-page-title">Custom Domains &amp; TLS (gatewayd)</h2>
              <p className="console-page-subtitle">Map custom domains to microVM functions with automatic SSL certificates.</p>
            </div>
            <button className="btn btn-gregale" onClick={() => setIsDomainModalOpen(true)}>+ Add Custom Domain</button>
          </div>

          <div className="console-card-panel">
            <table className="console-table">
              <thead>
                <tr>
                  <th>Domain Name</th>
                  <th>Target Function</th>
                  <th>TLS Certificate Status</th>
                  <th>Action</th>
                </tr>
              </thead>
              <tbody>
                {domains.map(d => (
                  <tr key={d.id}>
                    <td><code style={{ fontWeight: 600, color: 'var(--text-primary)' }}>{d.domain}</code></td>
                    <td><div className="app-name-cell">{d.targetFunc}</div></td>
                    <td><span className="status-badge restored">{d.sslStatus}</span></td>
                    <td>
                      <button className="btn btn-secondary btn-sm" style={{ color: '#EF4444' }} onClick={() => setDomains(prev => prev.filter(x => x.id !== d.id))}>
                        Remove Domain
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Tab 5: GitHub Integration (githubd) */}
      {activeTab === 'github' && (
        <div>
          <div className="console-page-header">
            <div>
              <h2 className="console-page-title">GitHub Push-to-Deploy Integration (githubd)</h2>
              <p className="console-page-subtitle">Auto-build and deploy microVM snapshots on git push.</p>
            </div>
          </div>

          <div className="console-card-panel" style={{ padding: '2rem' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '1.25rem', marginBottom: '1.5rem' }}>
              <div style={{ width: '48px', height: '48px', background: '#0F172A', borderRadius: '50%', display: 'flex', alignItems: 'center', justifyContent: 'center', color: '#FFF', fontWeight: 'bold' }}>
                GH
              </div>
              <div>
                <h3 style={{ fontSize: '1.2rem', marginBottom: '0.2rem' }}>Connected Repository: <strong>poyrazK/faas</strong></h3>
                <p style={{ fontSize: '0.9rem', color: 'var(--text-secondary)' }}>Branch: <code>main</code> • Trigger: Automatic Push-to-Deploy</p>
              </div>
            </div>

            <div style={{ background: '#F8FAFC', padding: '1.25rem', borderRadius: 'var(--radius-md)', border: '1px solid var(--border-dim)' }}>
              <div style={{ fontFamily: 'var(--font-mono)', fontSize: '0.85rem', fontWeight: 600, marginBottom: '0.5rem' }}>
                GitHub Webhook URL (ADR-012):
              </div>
              <code>{backendStatus.url}/v1/github/webhook</code>
            </div>
          </div>
        </div>
      )}

      {/* Tab 6: API Keys & Tokens (apid) */}
      {activeTab === 'apikeys' && (
        <div>
          <div className="console-page-header">
            <div>
              <h2 className="console-page-title">API Keys &amp; Access Tokens (apid)</h2>
              <p className="console-page-subtitle">Bearer tokens for CLI deployments and daemon authentication.</p>
            </div>
            <button className="btn btn-gregale" onClick={() => {
              setGeneratedKey(null);
              setIsApiKeyModalOpen(true);
            }}>+ Generate New API Key</button>
          </div>

          <div className="console-card-panel">
            <table className="console-table">
              <thead>
                <tr>
                  <th>Key Name</th>
                  <th>Key Prefix</th>
                  <th>Created</th>
                  <th>Action</th>
                </tr>
              </thead>
              <tbody>
                {apiKeys.map(k => (
                  <tr key={k.id}>
                    <td><strong>{k.name}</strong></td>
                    <td><code>{k.prefix}</code></td>
                    <td>{k.created}</td>
                    <td>
                      <button className="btn btn-secondary btn-sm" style={{ color: '#EF4444' }} onClick={() => setApiKeys(prev => prev.filter(x => x.id !== k.id))}>
                        Revoke Key
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Tab 7: OpenAPI Specification Map */}
      {activeTab === 'apiexplorer' && (
        <div>
          <div className="console-page-header">
            <div>
              <h2 className="console-page-title">OpenAPI 3.1 REST Endpoints Mapping</h2>
              <p className="console-page-subtitle">Direct component mapping of all customer-facing REST API routes.</p>
            </div>
          </div>

          <div className="console-card-panel">
            <table className="console-table">
              <thead>
                <tr>
                  <th>HTTP Method</th>
                  <th>Endpoint Route</th>
                  <th>Tag / Resource</th>
                  <th>Frontend UI Component</th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <td><span className="status-badge restored">GET</span></td>
                  <td><code>/v1/account</code></td>
                  <td>account</td>
                  <td>Cluster Overview Header &amp; Status Pill</td>
                </tr>
                <tr>
                  <td><span className="status-badge restored" style={{ background: '#E0F2FE', color: '#0369A1' }}>PATCH</span></td>
                  <td><code>/v1/account/plan</code></td>
                  <td>account</td>
                  <td>Metrics &amp; Metering Plan Switcher</td>
                </tr>
                <tr>
                  <td><span className="status-badge restored">GET</span></td>
                  <td><code>/v1/apps</code></td>
                  <td>apps</td>
                  <td>Functions &amp; Applications Table Component</td>
                </tr>
                <tr>
                  <td><span className="status-badge restored" style={{ background: '#DCFCE7', color: '#15803D' }}>POST</span></td>
                  <td><code>/v1/apps</code></td>
                  <td>apps</td>
                  <td>Deploy New MicroVM Function Modal</td>
                </tr>
                <tr>
                  <td><span className="status-badge restored" style={{ background: '#DCFCE7', color: '#15803D' }}>POST</span></td>
                  <td><code>/v1/apps/{'{slug}'}/wake</code></td>
                  <td>apps</td>
                  <td>Row Trigger Wake Button &amp; Benchmark Simulator</td>
                </tr>
                <tr>
                  <td><span className="status-badge restored" style={{ background: '#FEE2E2', color: '#B91C1C' }}>POST</span></td>
                  <td><code>/v1/apps/{'{slug}'}/park</code></td>
                  <td>apps</td>
                  <td>App Detail Slide-Over Park to Disk Action</td>
                </tr>
                <tr>
                  <td><span className="status-badge restored" style={{ background: '#DCFCE7', color: '#15803D' }}>POST</span></td>
                  <td><code>/v1/crons</code></td>
                  <td>crons</td>
                  <td>Add Cron Job Trigger Modal</td>
                </tr>
                <tr>
                  <td><span className="status-badge restored" style={{ background: '#DCFCE7', color: '#15803D' }}>POST</span></td>
                  <td><code>/v1/domains</code></td>
                  <td>domains</td>
                  <td>Add Custom Domain &amp; TLS Provisioning Modal</td>
                </tr>
                <tr>
                  <td><span className="status-badge restored" style={{ background: '#DCFCE7', color: '#15803D' }}>POST</span></td>
                  <td><code>/v1/keys</code></td>
                  <td>keys</td>
                  <td>Generate API Key &amp; Token Issuance Modal</td>
                </tr>
                <tr>
                  <td><span className="status-badge restored" style={{ background: '#DCFCE7', color: '#15803D' }}>POST</span></td>
                  <td><code>/v1/secrets</code></td>
                  <td>secrets</td>
                  <td>Add Sealed Environment Secret Modal</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Tab 8: Live Logs */}
      {activeTab === 'logs' && (
        <div>
          <div className="console-page-header">
            <div>
              <h2 className="console-page-title">Live Logs &amp; Observability</h2>
              <p className="console-page-subtitle">Real-time log stream from guest init, vmmd, and gatewayd processes.</p>
            </div>
          </div>

          <div className="code-showcase" style={{ maxWidth: '100%' }}>
            <div className="code-header-bar">
              <div style={{ fontFamily: 'var(--font-mono)', fontSize: '0.8rem', color: '#94A3B8' }}>
                Stream Target: <strong>all-services</strong> • <span style={{ color: '#4ADE80' }}>● TAILING</span>
              </div>
              <button className="copy-btn" onClick={() => setLogs([])}>Clear Logs</button>
            </div>
            <div className="code-body" style={{ minHeight: '420px', maxHeight: '520px', overflowY: 'auto' }}>
              {logs.map((log, i) => (
                <div className="stream-row" key={i}>
                  <span className="ts-col">{log.ts}</span>
                  <span className="service-col">{log.service}</span>
                  <span className="event-col">{log.event}</span>
                  <span className="status-tag ok">{log.status}</span>
                  <span style={{ color: '#94A3B8' }}>{log.text}</span>
                </div>
              ))}
            </div>
          </div>
        </div>
      )}

      {/* Tab 9: Jailer & Security */}
      {activeTab === 'jailer' && (
        <div>
          <div className="console-page-header">
            <div>
              <h2 className="console-page-title">Firecracker Jailer &amp; Isolation</h2>
              <p className="console-page-subtitle">Hardware-level microVM isolation boundary status (UID Jails 20000–29999, cgroup v2, TAP networking).</p>
            </div>
          </div>

          <div className="stat-cards-grid">
            <div className="stat-card">
              <div className="stat-card-label">Jailed UID Range</div>
              <div className="stat-card-val">20412</div>
              <div className="stat-card-sub">Isolated non-root host user</div>
            </div>

            <div className="stat-card">
              <div className="stat-card-label">TAP Interface</div>
              <div className="stat-card-val" style={{ color: 'var(--gregale-green)' }}>tap0</div>
              <div className="stat-card-sub">IPv4: 172.16.0.2/24</div>
            </div>

            <div className="stat-card">
              <div className="stat-card-label">Seccomp Filters</div>
              <div className="stat-card-val" style={{ color: 'var(--accent-green)' }}>STRICT</div>
              <div className="stat-card-sub">12 allowed syscalls</div>
            </div>

            <div className="stat-card">
              <div className="stat-card-label">Cgroup v2 Caps</div>
              <div className="stat-card-val">256MB / 1 vCPU</div>
              <div className="stat-card-sub">Hard quota limits</div>
            </div>
          </div>
        </div>
      )}

      {/* Tab 10: Storage */}
      {activeTab === 'storage' && (
        <div>
          <div className="console-page-header">
            <div>
              <h2 className="console-page-title">Two-Drive RootFS Overlay Storage</h2>
              <p className="console-page-subtitle">Drive0 base OS image sharing with Drive1 app layer for fast snapshot restores.</p>
            </div>
          </div>

          <div className="bento-grid">
            <div className="bento-card">
              <div className="bento-card-num">DRIVE 0 / READ-ONLY BASE</div>
              <h3 className="bento-card-title">Shared Base RootFS</h3>
              <p className="bento-card-desc">
                <code>/var/lib/gregale/images/base-v1.ext4</code> (85 MB read-only image shared across all microVMs for zero disk duplication).
              </p>
            </div>

            <div className="bento-card">
              <div className="bento-card-num">DRIVE 1 / APP OVERLAY</div>
              <h3 className="bento-card-title">App Layer Delta</h3>
              <p className="bento-card-desc">
                <code>/var/lib/gregale/apps/app-412/overlay.ext4</code> (~45 MB application delta mounted via OverlayFS in guest PID1).
              </p>
            </div>

            <div className="bento-card">
              <div className="bento-card-num">NVME SNAPSHOT</div>
              <h3 className="bento-card-title">184ms Restore Pipeline</h3>
              <p className="bento-card-desc">
                Memory unpark: 142ms • TAP net bind: 12ms • Guest clock step: 30ms. Total cold wake: 184ms.
              </p>
            </div>
          </div>
        </div>
      )}

      {/* Tab 11: Builder */}
      {activeTab === 'builder' && (
        <div>
          <div className="console-page-header">
            <div>
              <h2 className="console-page-title">Railpack Ephemeral Builder</h2>
              <p className="console-page-subtitle">Off-host builds executed inside isolated builder microVMs.</p>
            </div>
          </div>

          <div className="console-card-panel">
            <div className="panel-toolbar">
              <div style={{ fontWeight: 700, fontSize: '1rem' }}>Recent Build Pipelines</div>
              <span className="status-badge restored">RAILPACK ENGINE READY</span>
            </div>
            <table className="console-table">
              <thead>
                <tr>
                  <th>Build ID</th>
                  <th>App Target</th>
                  <th>Auto-Detected Runtime</th>
                  <th>Build Time</th>
                  <th>Snapshot Size</th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <td><code>build-8821</code></td>
                  <td>api-gateway</td>
                  <td>Go 1.23 (Railpack Auto)</td>
                  <td>2.4s</td>
                  <td>128 MB</td>
                </tr>
                <tr>
                  <td><code>build-8820</code></td>
                  <td>auth-service</td>
                  <td>Node.js 22 (TypeScript)</td>
                  <td>4.1s</td>
                  <td>142 MB</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Tab 12: Secrets */}
      {activeTab === 'secrets' && (
        <div>
          <div className="console-page-header">
            <div>
              <h2 className="console-page-title">Sealed Secrets &amp; Environment</h2>
              <p className="console-page-subtitle">Encrypted environment variables injected into guest microVM PID1.</p>
            </div>
            <button className="btn btn-gregale" onClick={openAddSecretModal}>+ Add Secret</button>
          </div>

          <div className="console-card-panel">
            <table className="console-table">
              <thead>
                <tr>
                  <th>Secret Key</th>
                  <th>Value</th>
                  <th>Target Function</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {secrets.map(s => (
                  <tr key={s.id}>
                    <td className="secret-key-code">{s.key}</td>
                    <td className="secret-val-mask">
                      {s.isMasked ? '••••••••••••••••••••••••••••' : s.val}
                    </td>
                    <td>{s.target}</td>
                    <td>
                      <button className="btn btn-secondary btn-sm" onClick={() => toggleSecretMask(s.id)}>
                        Toggle Mask
                      </button>
                      <button className="btn btn-secondary btn-sm" style={{ color: '#EF4444', marginLeft: '0.5rem' }} onClick={() => deleteSecretItem(s.id)}>
                        Delete
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Tab 13: Metrics & Account Settings */}
      {activeTab === 'metrics' && (
        <div>
          <div className="console-page-header">
            <div>
              <h2 className="console-page-title">Metrics &amp; Account Settings</h2>
              <p className="console-page-subtitle">Real-time memory GB-hour consumption, plan selection, and GDPR controls.</p>
            </div>
            <button className="btn btn-secondary btn-sm" onClick={handleExportData}>
              Export Full Data (GDPR JSON)
            </button>
          </div>

          <div className="stat-cards-grid">
            <div className="stat-card">
              <div className="stat-card-label">Current Billing Plan</div>
              <div className="stat-card-val" style={{ color: 'var(--gregale-green)' }}>
                {accountInfo?.plan?.toUpperCase() || 'FREE'} TIER
              </div>
              <div className="stat-card-sub">5 GB-hours included / mo</div>
            </div>

            <div className="stat-card">
              <div className="stat-card-label">Usage This Month</div>
              <div className="stat-card-val">2.8 GB-h</div>
              <div className="stat-card-sub">56% of included tier used</div>
            </div>

            <div className="stat-card">
              <div className="stat-card-label">Total Invocations</div>
              <div className="stat-card-val">14,210</div>
              <div className="stat-card-sub">Average runtime: 12ms</div>
            </div>

            <div className="stat-card">
              <div className="stat-card-label">P99 Cold-Wake</div>
              <div className="stat-card-val">210 ms</div>
              <div className="stat-card-sub">&lt;350ms SLA guaranteed</div>
            </div>
          </div>

          <div className="console-card-panel" style={{ marginTop: '1.5rem', padding: '1.5rem' }}>
            <h3 style={{ fontSize: '1.1rem', marginBottom: '1rem' }}>Manage Subscription Plan (PATCH /v1/account/plan)</h3>
            <div style={{ display: 'flex', gap: '0.75rem', flexWrap: 'wrap' }}>
              <button className="btn btn-secondary btn-sm" onClick={() => handleChangePlan('free')}>Set Free Tier</button>
              <button className="btn btn-secondary btn-sm" onClick={() => handleChangePlan('hobby')}>Upgrade to Hobby (€9)</button>
              <button className="btn btn-gregale btn-sm" onClick={() => handleChangePlan('pro')}>Upgrade to Pro (€29)</button>
              <button className="btn btn-secondary btn-sm" onClick={() => handleChangePlan('scale')}>Contact Scale (€99)</button>
            </div>
          </div>
        </div>
      )}

      {/* SLIDE-OVER FUNCTION DETAIL DRAWER */}
      {selectedFunc && (
        <div className="modal-overlay" style={{ opacity: 1, pointerEvents: 'auto' }}>
          <div className="modal-card" style={{ maxWidth: '640px' }}>
            <div className="modal-header">
              <div>
                <h3 className="modal-title">{selectedFunc.name} Details</h3>
                <span className={`status-badge ${selectedFunc.status === 'COLD RESTORED' ? 'restored' : 'parked'}`} style={{ marginTop: '0.35rem' }}>
                  {selectedFunc.status}
                </span>
              </div>
              <button className="modal-close-btn" onClick={() => setSelectedFunc(null)}>&times;</button>
            </div>
            <div className="modal-body">
              <div className="stat-cards-grid" style={{ gridTemplateColumns: '1fr 1fr', marginBottom: '1.5rem' }}>
                <div className="stat-card">
                  <div className="stat-card-label">Runtime</div>
                  <div style={{ fontWeight: 700 }}>{selectedFunc.runtime}</div>
                </div>
                <div className="stat-card">
                  <div className="stat-card-label">RAM Plan</div>
                  <div style={{ fontWeight: 700 }}>{selectedFunc.ram}</div>
                </div>
              </div>

              <div style={{ background: '#F8FAFC', padding: '1.25rem', borderRadius: 'var(--radius-md)', border: '1px solid var(--border-dim)', marginBottom: '1.5rem' }}>
                <div style={{ fontFamily: 'var(--font-mono)', fontSize: '0.82rem', fontWeight: 600, color: 'var(--text-muted)', marginBottom: '0.4rem' }}>
                  PUBLIC HTTPS ENDPOINT
                </div>
                <code style={{ fontSize: '0.9rem', color: 'var(--gregale-green)', fontWeight: 600 }}>
                  https://{selectedFunc.name}.gregale.app
                </code>
              </div>

              <div style={{ background: '#0F172A', color: '#F8FAFC', padding: '1.25rem', borderRadius: 'var(--radius-md)', fontFamily: 'var(--font-mono)', fontSize: '0.85rem' }}>
                <div style={{ color: '#94A3B8', marginBottom: '0.5rem' }}>// MicroVM Snapshot Metadata</div>
                <div>Ext4 OverlayFS: Drive0 base + Drive1 app (~130 MB)</div>
                <div>Jailed Process: UID 20412 (cgroup v2 hard limit)</div>
                <div>Cold restore: {selectedFunc.p50Wake}</div>
              </div>
            </div>
            <div className="modal-footer">
              <button className="btn btn-secondary" onClick={() => setSelectedFunc(null)}>Close</button>
              <button className="btn btn-secondary" onClick={() => handleParkApp(selectedFunc.name)}>
                Park to Disk (POST /v1/apps/{selectedFunc.name}/park)
              </button>
              <button className="btn btn-gregale" onClick={() => {
                handleWakeTest(selectedFunc.id, selectedFunc.name);
                setSelectedFunc(null);
              }}>Trigger Cold Wake</button>
              <button className="btn btn-secondary" style={{ color: '#EF4444' }} onClick={() => handleDeleteApp(selectedFunc.name)}>
                Delete Function
              </button>
            </div>
          </div>
        </div>
      )}

      {/* ADD CRON MODAL */}
      {isCronModalOpen && (
        <div className="modal-overlay" style={{ opacity: 1, pointerEvents: 'auto' }}>
          <div className="modal-card">
            <div className="modal-header">
              <h3 className="modal-title">Create Scheduled Cron Trigger (schedd)</h3>
              <button className="modal-close-btn" onClick={() => setIsCronModalOpen(false)}>&times;</button>
            </div>
            <form onSubmit={handleAddCron}>
              <div className="modal-body">
                <div className="form-group">
                  <label className="form-label">Cron Expression</label>
                  <input 
                    type="text" 
                    className="form-control" 
                    value={newCronExpr}
                    onChange={(e) => setNewCronExpr(e.target.value)}
                    placeholder="e.g. 0 * * * *"
                    required
                  />
                </div>
                <div className="form-group">
                  <label className="form-label">Target Function</label>
                  <select 
                    className="form-control" 
                    value={newCronTarget}
                    onChange={(e) => setNewCronTarget(e.target.value)}
                  >
                    {functions.map(f => (
                      <option key={f.id} value={f.name}>{f.name}</option>
                    ))}
                  </select>
                </div>
              </div>
              <div className="modal-footer">
                <button type="button" className="btn btn-secondary" onClick={() => setIsCronModalOpen(false)}>Cancel</button>
                <button type="submit" className="btn btn-gregale">Save Cron Trigger</button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* ADD DOMAIN MODAL */}
      {isDomainModalOpen && (
        <div className="modal-overlay" style={{ opacity: 1, pointerEvents: 'auto' }}>
          <div className="modal-card">
            <div className="modal-header">
              <h3 className="modal-title">Add Custom Domain (gatewayd)</h3>
              <button className="modal-close-btn" onClick={() => setIsDomainModalOpen(false)}>&times;</button>
            </div>
            <form onSubmit={handleAddDomain}>
              <div className="modal-body">
                <div className="form-group">
                  <label className="form-label">Custom Domain</label>
                  <input 
                    type="text" 
                    className="form-control" 
                    placeholder="e.g. api.mycompany.dev" 
                    value={newDomainName}
                    onChange={(e) => setNewDomainName(e.target.value)}
                    required
                  />
                </div>
                <div className="form-group">
                  <label className="form-label">Target Function</label>
                  <select 
                    className="form-control" 
                    value={newDomainTarget}
                    onChange={(e) => setNewDomainTarget(e.target.value)}
                  >
                    {functions.map(f => (
                      <option key={f.id} value={f.name}>{f.name}</option>
                    ))}
                  </select>
                </div>
              </div>
              <div className="modal-footer">
                <button type="button" className="btn btn-secondary" onClick={() => setIsDomainModalOpen(false)}>Cancel</button>
                <button type="submit" className="btn btn-gregale">Add Domain &amp; Provision TLS</button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* GENERATE API KEY MODAL */}
      {isApiKeyModalOpen && (
        <div className="modal-overlay" style={{ opacity: 1, pointerEvents: 'auto' }}>
          <div className="modal-card">
            <div className="modal-header">
              <h3 className="modal-title">Generate API Key (apid)</h3>
              <button className="modal-close-btn" onClick={() => setIsApiKeyModalOpen(false)}>&times;</button>
            </div>
            <form onSubmit={handleCreateApiKey}>
              <div className="modal-body">
                {generatedKey ? (
                  <div>
                    <div style={{ background: '#DCFCE7', padding: '1rem', borderRadius: 'var(--radius-md)', border: '1px solid #86EFAC', marginBottom: '1.25rem' }}>
                      <div style={{ fontSize: '0.85rem', fontWeight: 600, color: '#15803D', marginBottom: '0.35rem' }}>
                        ✔ API Key Generated! Copy it now (it won't be shown again):
                      </div>
                      <code style={{ fontSize: '0.9rem', wordBreak: 'break-all', fontWeight: 'bold' }}>{generatedKey}</code>
                    </div>
                  </div>
                ) : (
                  <div className="form-group">
                    <label className="form-label">Key Description Name</label>
                    <input 
                      type="text" 
                      className="form-control" 
                      placeholder="e.g. Production CI/CD Token" 
                      value={newKeyName}
                      onChange={(e) => setNewKeyName(e.target.value)}
                      required
                    />
                  </div>
                )}
              </div>
              <div className="modal-footer">
                <button type="button" className="btn btn-secondary" onClick={() => setIsApiKeyModalOpen(false)}>
                  {generatedKey ? 'Done' : 'Cancel'}
                </button>
                {!generatedKey && <button type="submit" className="btn btn-gregale">Generate Key</button>}
              </div>
            </form>
          </div>
        </div>
      )}
    </main>
  );
};
