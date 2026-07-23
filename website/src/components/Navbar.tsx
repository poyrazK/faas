'use client';

import React from 'react';

interface NavbarProps {
  viewMode: 'landing' | 'console';
  setViewMode: (mode: 'landing' | 'console') => void;
}

export const Navbar: React.FC<NavbarProps> = ({ viewMode, setViewMode }) => {
  return (
    <header>
      <nav className="navbar">
        <div className="container">
          <a href="#" className="nav-brand" onClick={(e) => { e.preventDefault(); setViewMode('landing'); }}>
            <img 
              src="/gregale-logo-green-trans.png" 
              alt="Gregale" 
              className="nav-brand-logo-img" 
            />
          </a>

          <ul className="nav-links">
            <li>
              <a 
                href="#landing" 
                className="nav-link" 
                onClick={(e) => { e.preventDefault(); setViewMode('landing'); }}
              >
                Landing
              </a>
            </li>
            {viewMode === 'landing' && (
              <>
                <li><a href="#architecture" className="nav-link">Architecture</a></li>
                <li><a href="#benchmark" className="nav-link">Benchmark</a></li>
                <li><a href="#pricing" className="nav-link">Pricing</a></li>
              </>
            )}
            <li>
              <a 
                href="#console" 
                className="nav-link" 
                style={{ color: 'var(--gregale-green)', fontWeight: 700 }}
                onClick={(e) => { e.preventDefault(); setViewMode('console'); }}
              >
                Console
              </a>
            </li>
          </ul>

          <div>
            <button 
              className="btn btn-gregale" 
              onClick={() => setViewMode(viewMode === 'landing' ? 'console' : 'landing')}
            >
              {viewMode === 'landing' ? 'Open Console' : 'View Landing Page'}
            </button>
          </div>
        </div>
      </nav>
    </header>
  );
};
