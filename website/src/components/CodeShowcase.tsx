'use client';

import React, { useState } from 'react';

type TabKey = 'go' | 'ts' | 'py' | 'cli';

export const CodeShowcase: React.FC = () => {
  const [activeTab, setActiveTab] = useState<TabKey>('go');
  const [copied, setCopied] = useState(false);

  const snippets: Record<TabKey, string> = {
    go: `package main

import (
    "fmt"
    "net/http"
)

// Handle runs inside an isolated Firecracker MicroVM snapshot
func Handle(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("x-gregale-wake", "cold")
    fmt.Fprintf(w, "Hello from Gregale MicroVM! Execution duration: <10ms\\n")
}`,
    ts: `import { defineFunction } from "@gregale/sdk";

// Served via snapshot unparking in <350ms
export default defineFunction(async (req) => {
  return new Response(
    JSON.stringify({ message: "Hello from Gregale MicroVM!", status: "cold-wake" }),
    { headers: { "content-type": "application/json" } }
  );
});`,
    py: `# Unparks from NVMe snapshot with 0 MB resident RAM when idle
def handler(event, context):
    return {
        "statusCode": 200,
        "headers": { "x-gregale-wake": "cold" },
        "body": "Hello from Gregale MicroVM! Unparked in 184ms."
    }`,
    cli: `# Deploy serverless microVM function in 3 seconds
$ gregale auth login
$ gregale app create my-api --ram 256MB
$ gregale deploy --dir ./src

✔ Initializing ephemeral builder microVM
✔ Generating Firecracker snapshot overlay (128 MB)
✔ Deployed to production: https://my-api.gregale.app`
  };

  const handleCopy = () => {
    navigator.clipboard.writeText(snippets[activeTab]);
    setCopied(true);
    setTimeout(() => setCopied(false), 1800);
  };

  return (
    <div className="code-showcase" id="code">
      <div className="code-header-bar">
        <ul className="code-tabs">
          <li>
            <button 
              className={`code-tab-btn ${activeTab === 'go' ? 'active' : ''}`} 
              onClick={() => setActiveTab('go')}
            >
              Go 1.23
            </button>
          </li>
          <li>
            <button 
              className={`code-tab-btn ${activeTab === 'ts' ? 'active' : ''}`} 
              onClick={() => setActiveTab('ts')}
            >
              TypeScript / Node
            </button>
          </li>
          <li>
            <button 
              className={`code-tab-btn ${activeTab === 'py' ? 'active' : ''}`} 
              onClick={() => setActiveTab('py')}
            >
              Python 3.12
            </button>
          </li>
          <li>
            <button 
              className={`code-tab-btn ${activeTab === 'cli' ? 'active' : ''}`} 
              onClick={() => setActiveTab('cli')}
            >
              Gregale CLI
            </button>
          </li>
        </ul>

        <div>
          <button className="copy-btn" onClick={handleCopy}>
            {copied ? 'Copied!' : 'Copy Snippet'}
          </button>
        </div>
      </div>

      <div className="code-body">
        <pre className="code-snippet active">
          <code>{snippets[activeTab]}</code>
        </pre>
      </div>
    </div>
  );
};
