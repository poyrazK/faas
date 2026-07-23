'use client';

import React from 'react';

export type ConsoleTab = 
  | 'overview' 
  | 'functions' 
  | 'logs' 
  | 'crons'
  | 'domains'
  | 'github'
  | 'apikeys'
  | 'apiexplorer'
  | 'jailer' 
  | 'storage' 
  | 'builder' 
  | 'secrets' 
  | 'metrics';

interface ConsoleSidebarProps {
  activeTab: ConsoleTab;
  setActiveTab: (tab: ConsoleTab) => void;
  vmsCount: number;
}

export const ConsoleSidebar: React.FC<ConsoleSidebarProps> = ({ 
  activeTab, 
  setActiveTab,
  vmsCount
}) => {
  return (
    <aside className="console-sidebar">
      <div>
        <div className="sidebar-group">
          <div className="sidebar-title">Core Management</div>
          <ul className="sidebar-menu">
            <li>
              <button 
                className={`sidebar-btn ${activeTab === 'overview' ? 'active' : ''}`}
                onClick={() => setActiveTab('overview')}
              >
                <span>Overview</span>
              </button>
            </li>
            <li>
              <button 
                className={`sidebar-btn ${activeTab === 'functions' ? 'active' : ''}`}
                onClick={() => setActiveTab('functions')}
              >
                <span>Functions</span>
                <span className="sidebar-badge green">{vmsCount}</span>
              </button>
            </li>
            <li>
              <button 
                className={`sidebar-btn ${activeTab === 'crons' ? 'active' : ''}`}
                onClick={() => setActiveTab('crons')}
              >
                <span>Cron Triggers</span>
                <span className="sidebar-badge">Crons</span>
              </button>
            </li>
            <li>
              <button 
                className={`sidebar-btn ${activeTab === 'logs' ? 'active' : ''}`}
                onClick={() => setActiveTab('logs')}
              >
                <span>Live Logs</span>
                <span className="sidebar-badge">Live</span>
              </button>
            </li>
          </ul>
        </div>

        <div className="sidebar-group">
          <div className="sidebar-title">Networking &amp; Git</div>
          <ul className="sidebar-menu">
            <li>
              <button 
                className={`sidebar-btn ${activeTab === 'domains' ? 'active' : ''}`}
                onClick={() => setActiveTab('domains')}
              >
                <span>Custom Domains &amp; TLS</span>
              </button>
            </li>
            <li>
              <button 
                className={`sidebar-btn ${activeTab === 'github' ? 'active' : ''}`}
                onClick={() => setActiveTab('github')}
              >
                <span>GitHub Push-Deploy</span>
              </button>
            </li>
            <li>
              <button 
                className={`sidebar-btn ${activeTab === 'apikeys' ? 'active' : ''}`}
                onClick={() => setActiveTab('apikeys')}
              >
                <span>API Keys &amp; Tokens</span>
              </button>
            </li>
            <li>
              <button 
                className={`sidebar-btn ${activeTab === 'apiexplorer' ? 'active' : ''}`}
                onClick={() => setActiveTab('apiexplorer')}
              >
                <span>OpenAPI Explorer</span>
                <span className="sidebar-badge green">v1.0</span>
              </button>
            </li>
          </ul>
        </div>

        <div className="sidebar-group">
          <div className="sidebar-title">Engine Features</div>
          <ul className="sidebar-menu">
            <li>
              <button 
                className={`sidebar-btn ${activeTab === 'jailer' ? 'active' : ''}`}
                onClick={() => setActiveTab('jailer')}
              >
                <span>Jailer &amp; Security</span>
                <span className="sidebar-badge">UID 20K</span>
              </button>
            </li>
            <li>
              <button 
                className={`sidebar-btn ${activeTab === 'storage' ? 'active' : ''}`}
                onClick={() => setActiveTab('storage')}
              >
                <span>2-Drive OverlayFS</span>
              </button>
            </li>
            <li>
              <button 
                className={`sidebar-btn ${activeTab === 'builder' ? 'active' : ''}`}
                onClick={() => setActiveTab('builder')}
              >
                <span>Railpack Builder</span>
              </button>
            </li>
          </ul>
        </div>

        <div className="sidebar-group">
          <div className="sidebar-title">Configuration</div>
          <ul className="sidebar-menu">
            <li>
              <button 
                className={`sidebar-btn ${activeTab === 'secrets' ? 'active' : ''}`}
                onClick={() => setActiveTab('secrets')}
              >
                <span>Sealed Secrets</span>
              </button>
            </li>
            <li>
              <button 
                className={`sidebar-btn ${activeTab === 'metrics' ? 'active' : ''}`}
                onClick={() => setActiveTab('metrics')}
              >
                <span>Metrics &amp; Billing</span>
              </button>
            </li>
          </ul>
        </div>
      </div>

      <div style={{ paddingTop: '1rem', borderTop: '1px solid var(--border-dim)' }}>
        <div style={{ fontFamily: 'var(--font-mono)', fontSize: '0.75rem', color: 'var(--text-muted)', marginBottom: '0.35rem' }}>
          REGIONAL CLUSTER
        </div>
        <div style={{ fontSize: '0.85rem', fontWeight: 600, color: 'var(--text-primary)', display: 'flex', alignItems: 'center', gap: '0.35rem' }}>
          <span style={{ width: '8px', height: '8px', background: 'var(--accent-green)', borderRadius: '50%' }}></span>
          hetzner-fsn1-ex44
        </div>
      </div>
    </aside>
  );
};
