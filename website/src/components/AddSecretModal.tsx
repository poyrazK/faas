'use client';

import React, { useState } from 'react';
import { SecretItem } from './ConsoleWorkspace';

interface AddSecretModalProps {
  isOpen: boolean;
  onClose: () => void;
  onAddSecret: (secret: SecretItem) => void;
}

export const AddSecretModal: React.FC<AddSecretModalProps> = ({
  isOpen,
  onClose,
  onAddSecret,
}) => {
  const [key, setKey] = useState('');
  const [val, setVal] = useState('');
  const [target, setTarget] = useState('Global (All Functions)');

  if (!isOpen) return null;

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!key.trim()) return;

    onAddSecret({
      id: `secret-${Date.now()}`,
      key: key.trim(),
      val: val.trim() || 'sk_live_941a82f0412b5912c41d99',
      target,
      isMasked: true,
    });

    setKey('');
    setVal('');
    onClose();
  };

  return (
    <div className="modal-overlay">
      <div className="modal-card">
        <div className="modal-header">
          <h3 className="modal-title">Add Sealed Environment Secret</h3>
          <button className="modal-close-btn" onClick={onClose}>&times;</button>
        </div>
        <form onSubmit={handleSubmit}>
          <div className="modal-body">
            <div className="form-group">
              <label className="form-label">Secret Key Name</label>
              <input 
                type="text" 
                className="form-control" 
                placeholder="e.g. AWS_SECRET_ACCESS_KEY" 
                value={key}
                onChange={(e) => setKey(e.target.value)}
                required
              />
            </div>

            <div className="form-group">
              <label className="form-label">Secret Value</label>
              <input 
                type="password" 
                className="form-control" 
                placeholder="Value will be encrypted on disk" 
                value={val}
                onChange={(e) => setVal(e.target.value)}
                required
              />
            </div>

            <div className="form-group">
              <label className="form-label">Scope / Target Function</label>
              <select 
                className="form-control" 
                value={target}
                onChange={(e) => setTarget(e.target.value)}
              >
                <option value="Global (All Functions)">Global (All Functions)</option>
                <option value="api-gateway">api-gateway</option>
                <option value="auth-service">auth-service</option>
                <option value="stripe-webhook">stripe-webhook</option>
              </select>
            </div>
          </div>
          <div className="modal-footer">
            <button type="button" className="btn btn-secondary" onClick={onClose}>Cancel</button>
            <button type="submit" className="btn btn-gregale">Save Encrypted Secret</button>
          </div>
        </form>
      </div>
    </div>
  );
};
