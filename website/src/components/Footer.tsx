'use client';

import React from 'react';

export const Footer: React.FC = () => {
  return (
    <footer>
      <div className="container">
        <div className="footer-grid">
          <div className="footer-brand">
            <img src="/gregale-logo-green-trans.png" alt="Gregale" style={{ height: '38px', width: 'auto', marginBottom: '0.85rem' }} />
            <p>
              Gregale is a scale-to-zero Functions-as-a-Service platform powered by Firecracker microVMs on bare-metal Hetzner EX44 infrastructure.
            </p>
          </div>

          <div>
            <h4 className="footer-col-title">PLATFORM</h4>
            <ul className="footer-col-links">
              <li><a href="#code">Code Showcase</a></li>
              <li><a href="#architecture">Architecture</a></li>
              <li><a href="#benchmark">Benchmark</a></li>
              <li><a href="#pricing">Pricing</a></li>
            </ul>
          </div>

          <div>
            <h4 className="footer-col-title">CLI &amp; SPECS</h4>
            <ul className="footer-col-links">
              <li><a href="#">CLI Reference</a></li>
              <li><a href="#">GitHub Push-to-Deploy</a></li>
              <li><a href="#">Sealed Secrets</a></li>
            </ul>
          </div>

          <div>
            <h4 className="footer-col-title">LEGAL</h4>
            <ul className="footer-col-links">
              <li><a href="#">Status Page</a></li>
              <li><a href="#">Privacy Policy</a></li>
              <li><a href="#">DPA</a></li>
            </ul>
          </div>
        </div>

        <div className="footer-bottom">
          <div>&copy; 2026 Gregale. All rights reserved.</div>
          <div>Scale-to-zero Firecracker microVMs</div>
        </div>
      </div>
    </footer>
  );
};
