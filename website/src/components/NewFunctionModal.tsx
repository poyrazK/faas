'use client';

import React, { useState } from 'react';
import { MicroVMFunction } from './ConsoleWorkspace';

interface NewFunctionModalProps {
  isOpen: boolean;
  onClose: () => void;
  onAddFunction: (func: MicroVMFunction) => void;
}

export const NewFunctionModal: React.FC<NewFunctionModalProps> = ({
  isOpen,
  onClose,
  onAddFunction,
}) => {
  const [name, setName] = useState('');
  const [runtime, setRuntime] = useState('Go 1.23');
  const [ram, setRam] = useState('256 MB');

  if (!isOpen) return null;

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;

    onAddFunction({
      id: `func-${Date.now()}`,
      name: name.trim(),
      runtime,
      status: 'COLD RESTORED',
      ram,
      p50Wake: '165 ms',
      region: 'hetzner-fsn1',
      lastExecuted: 'Just now',
    });

    setName('');
    onClose();
  };

  return (
    <div className="modal-overlay">
      <div className="modal-card">
        <div className="modal-header">
          <h3 className="modal-title">Deploy New MicroVM Function</h3>
          <button className="modal-close-btn" onClick={onClose}>&times;</button>
        </div>
        <form onSubmit={handleSubmit}>
          <div className="modal-body">
            <div className="form-group">
              <label className="form-label">Function Name</label>
              <input 
                type="text" 
                className="form-control" 
                placeholder="e.g. payment-processor" 
                value={name}
                onChange={(e) => setName(e.target.value)}
                required
              />
            </div>

            <div className="form-group">
              <label className="form-label">Runtime Engine</label>
              <select 
                className="form-control" 
                value={runtime}
                onChange={(e) => setRuntime(e.target.value)}
              >
                <option value="Go 1.23">Go 1.23</option>
                <option value="Node.js 22">Node.js 22 (TypeScript)</option>
                <option value="Python 3.12">Python 3.12</option>
                <option value="Rust 1.80">Rust 1.80</option>
                <option value="Dockerfile">Custom Dockerfile</option>
              </select>
            </div>

            <div className="form-group">
              <label className="form-label">RAM Memory Plan</label>
              <select 
                className="form-control" 
                value={ram}
                onChange={(e) => setRam(e.target.value)}
              >
                <option value="128 MB">128 MB (Sub-150ms Cold Wake)</option>
                <option value="256 MB">256 MB (Recommended)</option>
                <option value="512 MB">512 MB</option>
                <option value="1024 MB">1024 MB (1 GB)</option>
              </select>
            </div>
          </div>
          <div className="modal-footer">
            <button type="button" className="btn btn-secondary" onClick={onClose}>Cancel</button>
            <button type="submit" className="btn btn-gregale">Deploy to Production</button>
          </div>
        </form>
      </div>
    </div>
  );
};
