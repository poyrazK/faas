'use client';

import React, { useState } from 'react';

export const ColdWakeBenchmark: React.FC = () => {
  const [isSimulating, setIsSimulating] = useState(false);
  const [latency, setLatency] = useState(310);
  const [barWidth, setBarWidth] = useState('15%');

  const runSimulation = () => {
    if (isSimulating) return;
    setIsSimulating(true);
    setBarWidth('0%');
    setLatency(0);

    setTimeout(() => {
      setBarWidth('15%');
      setLatency(310);
      setIsSimulating(false);
    }, 600);
  };

  return (
    <section className="section" id="benchmark">
      <div className="container">
        <div className="section-label">BENCHMARK</div>
        <h2 className="section-title">Sub-350ms Cold Wake Benchmark</h2>
        <p className="section-subtitle">
          Firecracker snapshot unparking eliminates multi-second container image pulling. Test simulated HTTP wake latency below.
        </p>

        <div className="bench-card">
          <div className="bench-header-row">
            <div style={{ fontFamily: 'var(--font-mono)', fontSize: '0.9rem', fontWeight: 600 }}>
              HTTP Request Cold-Wake Latency (p50)
            </div>
            <button 
              className="btn btn-gregale" 
              onClick={runSimulation}
              disabled={isSimulating}
              style={{ padding: '0.4rem 0.85rem', fontSize: '0.82rem' }}
            >
              {isSimulating ? 'Simulating...' : 'Simulate Request Trigger'}
            </button>
          </div>

          <div className="bench-row">
            <div className="bench-lbl">
              <span style={{ color: 'var(--gregale-green)' }}>
                Gregale MicroVM (Firecracker Snapshot Restore)
              </span>
              <span>{latency} ms</span>
            </div>
            <div className="bench-track">
              <div className="bench-bar-gregale" style={{ width: barWidth }}>
                {latency}ms
              </div>
            </div>
          </div>

          <div className="bench-row">
            <div className="bench-lbl">
              <span style={{ color: 'var(--text-muted)' }}>
                Traditional Container FaaS (Docker Cold Boot)
              </span>
              <span>3,800 ms</span>
            </div>
            <div className="bench-track">
              <div className="bench-bar-trad" style={{ width: '95%' }}>
                3,800ms
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
};
