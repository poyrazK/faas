'use client';

import React, { useState } from 'react';

interface UsageEstimatorProps {
  onSelectPlan?: (plan: string) => void;
}

export const UsageEstimator: React.FC<UsageEstimatorProps> = ({ onSelectPlan }) => {
  const [ramMB, setRamMB] = useState(256);
  const [hoursMonth, setHoursMonth] = useState(300);

  const ramGB = ramMB / 1024;
  const gbHours = Math.round(ramGB * hoursMonth);

  let recommendedPlan = 'Free';
  let basePrice = 0;
  let includedGBH = 5;

  if (gbHours > 250 || ramMB > 512) {
    recommendedPlan = 'Pro';
    basePrice = 29;
    includedGBH = 250;
  } else if (gbHours > 50 || ramMB > 256) {
    recommendedPlan = 'Pro';
    basePrice = 29;
    includedGBH = 250;
  } else if (gbHours > 5 || ramMB > 128) {
    recommendedPlan = 'Hobby';
    basePrice = 9;
    includedGBH = 50;
  }

  const overageGBH = Math.max(0, gbHours - includedGBH);
  const overageCost = overageGBH * 0.01;
  const totalCost = basePrice + overageCost;

  return (
    <section className="section" id="pricing">
      <div className="container">
        <div className="section-label">PRICING</div>
        <h2 className="section-title">Transparent Monthly Plans</h2>
        <p className="section-subtitle">
          5 GB-hours included every month on Free. Predictable linear pricing on paid plans.
        </p>

        <div className="pricing-grid">
          {/* Free */}
          <div className="plan-card">
            <div>
              <h3 className="plan-title">Free</h3>
              <div className="plan-price">€0 <span>/ mo</span></div>
              <ul className="plan-specs">
                <li>✓ 5 GB-hours / mo</li>
                <li>✓ 1 Deployed App</li>
                <li>✓ 128 MB RAM / app</li>
              </ul>
            </div>
            <button className="btn btn-secondary" onClick={() => onSelectPlan && onSelectPlan('Free')}>
              Get Started
            </button>
          </div>

          {/* Hobby */}
          <div className="plan-card featured">
            <div>
              <h3 className="plan-title">Hobby</h3>
              <div className="plan-price">€9 <span>/ mo</span></div>
              <ul className="plan-specs">
                <li>✓ 50 GB-hours / mo</li>
                <li>✓ 5 Deployed Apps</li>
                <li>✓ 256 MB RAM / app</li>
              </ul>
            </div>
            <button className="btn btn-gregale" onClick={() => onSelectPlan && onSelectPlan('Hobby')}>
              Deploy Hobby Plan
            </button>
          </div>

          {/* Pro */}
          <div className="plan-card">
            <div>
              <h3 className="plan-title">Pro</h3>
              <div className="plan-price">€29 <span>/ mo</span></div>
              <ul className="plan-specs">
                <li>✓ 250 GB-hours / mo</li>
                <li>✓ 25 Deployed Apps</li>
                <li>✓ Custom Domains &amp; TLS</li>
              </ul>
            </div>
            <button className="btn btn-secondary" onClick={() => onSelectPlan && onSelectPlan('Pro')}>
              Upgrade Pro
            </button>
          </div>

          {/* Scale */}
          <div className="plan-card">
            <div>
              <h3 className="plan-title">Scale</h3>
              <div className="plan-price">€99 <span>/ mo</span></div>
              <ul className="plan-specs">
                <li>✓ 1,500 GB-hours / mo</li>
                <li>✓ 100 Deployed Apps</li>
                <li>✓ Warm Keepalive</li>
              </ul>
            </div>
            <button className="btn btn-secondary" onClick={() => onSelectPlan && onSelectPlan('Scale')}>
              Contact Scale
            </button>
          </div>
        </div>

        {/* Calculator */}
        <div className="calc-panel">
          <h3 style={{ fontFamily: 'var(--font-mono)', fontSize: '1.1rem', marginBottom: '1.25rem' }}>
            GB-Hour Usage Estimator
          </h3>

          <div className="calc-row">
            <div className="calc-lbl-bar">
              <span>RAM Allocation</span>
              <span style={{ color: 'var(--gregale-green)' }}>{ramMB} MB</span>
            </div>
            <input 
              type="range" 
              min="128" 
              max="1024" 
              step="128" 
              value={ramMB} 
              onChange={(e) => setRamMB(parseInt(e.target.value, 10))}
            />
          </div>

          <div className="calc-row">
            <div className="calc-lbl-bar">
              <span>Active Execution Duration</span>
              <span style={{ color: 'var(--gregale-green)' }}>{hoursMonth.toLocaleString()} hrs/mo</span>
            </div>
            <input 
              type="range" 
              min="10" 
              max="720" 
              step="10" 
              value={hoursMonth} 
              onChange={(e) => setHoursMonth(parseInt(e.target.value, 10))}
            />
          </div>

          <div className="calc-footer">
            <div>
              <div style={{ fontSize: '0.85rem', color: 'var(--text-secondary)' }}>Calculated Consumption:</div>
              <div style={{ fontSize: '1.1rem', fontWeight: 700 }}>{gbHours.toLocaleString()} GB-h</div>
              <div style={{ fontSize: '0.8rem', color: 'var(--gregale-green)' }}>{recommendedPlan} Plan</div>
            </div>

            <div style={{ textAlign: 'right' }}>
              <div style={{ fontSize: '0.85rem', color: 'var(--text-secondary)' }}>Estimated Total</div>
              <div className="calc-total">€{totalCost.toFixed(2)}</div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
};
