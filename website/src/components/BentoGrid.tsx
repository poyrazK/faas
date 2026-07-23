'use client';

import React from 'react';

export const BentoGrid: React.FC = () => {
  return (
    <section className="section" id="architecture">
      <div className="container">
        <div className="section-label">ARCHITECTURE</div>
        <h2 className="section-title">Engineered for Hardware-Level Security</h2>
        <p className="section-subtitle">
          Every layer of Gregale is built on bare-metal Hetzner EX44 servers to eliminate shared state and zero resident RAM while idle.
        </p>

        <div className="bento-grid">
          <div className="bento-card wide">
            <div className="bento-card-num">01 / MICROVM ISOLATION</div>
            <h3 className="bento-card-title">Hardware MicroVM Boundaries</h3>
            <p className="bento-card-desc">
              No container leaks or shared host directories. Every function runs inside its own Firecracker microVM with dedicated kernel, jailed UID/GID (20000–29999), TAP interface, cgroup v2 caps, and seccomp filters.
            </p>
          </div>

          <div className="bento-card">
            <div className="bento-card-num">02 / DISK ECONOMICS</div>
            <h3 className="bento-card-title">Two-Drive RootFS Overlay</h3>
            <p className="bento-card-desc">
              Drive0 contains shared read-only base images; Drive1 stores your app layer. OverlayFS in guest PID1 keeps average snapshot size to ~130 MB for rapid NVMe restores.
            </p>
          </div>

          <div className="bento-card">
            <div className="bento-card-num">03 / BILLING MATH</div>
            <h3 className="bento-card-title">Linear €0.01 / GB-h Metering</h3>
            <p className="bento-card-desc">
              Billing uses configured plan RAM + 8 MB system overhead per running second. Zero charges when parked on disk.
            </p>
          </div>

          <div className="bento-card wide">
            <div className="bento-card-num">04 / ZERO-CONFIG BUILD</div>
            <h3 className="bento-card-title">Ephemeral Builder MicroVMs</h3>
            <p className="bento-card-desc">
              Builds run inside ephemeral builder microVMs (never host containers). Railpack auto-detects Node.js 22, Python 3.12, Go 1.23, or custom Dockerfiles safely off-host.
            </p>
          </div>
        </div>
      </div>
    </section>
  );
};
