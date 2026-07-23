/* ==========================================================================
   Gregale - Cloud Provider Management Console & Landing Logic
   ========================================================================== */

document.addEventListener('DOMContentLoaded', () => {
  initViewSwitcher();
  initConsoleSidebar();
  initFunctionManager();
  initSecretsManager();
  initModals();
  initCodeShowcaseTabs();
  initBenchmarkCard();
  initPricingCalculator();
  initCopyButtons();
});

/* --------------------------------------------------------------------------
 * 1. View Switcher (Landing Page vs Cloud Provider Console)
 * -------------------------------------------------------------------------- */
function initViewSwitcher() {
  const landingPanel = document.getElementById('landing-view-panel');
  const consolePanel = document.getElementById('cloud-console-panel');

  const openConsoleBtn = document.getElementById('open-console-btn');
  const heroOpenConsole = document.getElementById('hero-open-console');
  const brandHomeLink = document.getElementById('brand-home-link');
  const navLinkLanding = document.getElementById('nav-link-landing');
  const navLinkConsoleTab = document.getElementById('nav-link-console-tab');
  const launchConsolePlanBtns = document.querySelectorAll('.launch-console-plan');

  function showConsole() {
    landingPanel.classList.add('hidden');
    consolePanel.classList.remove('hidden');
    window.scrollTo({ top: 0, behavior: 'smooth' });
  }

  function showLanding() {
    consolePanel.classList.add('hidden');
    landingPanel.classList.remove('hidden');
    window.scrollTo({ top: 0, behavior: 'smooth' });
  }

  if (openConsoleBtn) openConsoleBtn.addEventListener('click', showConsole);
  if (heroOpenConsole) heroOpenConsole.addEventListener('click', showConsole);
  if (navLinkConsoleTab) navLinkConsoleTab.addEventListener('click', (e) => { e.preventDefault(); showConsole(); });
  if (brandHomeLink) brandHomeLink.addEventListener('click', (e) => { e.preventDefault(); showLanding(); });
  if (navLinkLanding) navLinkLanding.addEventListener('click', (e) => { e.preventDefault(); showLanding(); });

  launchConsolePlanBtns.forEach(btn => {
    btn.addEventListener('click', showConsole);
  });
}

/* --------------------------------------------------------------------------
 * 2. Console Sidebar Navigation
 * -------------------------------------------------------------------------- */
function initConsoleSidebar() {
  const sidebarBtns = document.querySelectorAll('.sidebar-btn');
  const consoleTabContents = document.querySelectorAll('.console-tab-content');
  const viewAllFuncsBtn = document.getElementById('view-all-funcs-btn');

  sidebarBtns.forEach(btn => {
    btn.addEventListener('click', () => {
      const targetTab = btn.getAttribute('data-console-tab');
      if (!targetTab) return;

      sidebarBtns.forEach(b => b.classList.remove('active'));
      btn.classList.add('active');

      consoleTabContents.forEach(content => {
        if (content.id === `c-tab-${targetTab}`) {
          content.classList.add('active');
        } else {
          content.classList.remove('active');
        }
      });
    });
  });

  if (viewAllFuncsBtn) {
    viewAllFuncsBtn.addEventListener('click', () => {
      const funcsSidebarBtn = document.querySelector('.sidebar-btn[data-console-tab="functions"]');
      if (funcsSidebarBtn) funcsSidebarBtn.click();
    });
  }
}

/* --------------------------------------------------------------------------
 * 3. Function Manager & Cold Wake Simulator
 * -------------------------------------------------------------------------- */
function initFunctionManager() {
  const searchInput = document.getElementById('func-search-input');
  const tableBody = document.getElementById('functions-table-body');
  const logStream = document.getElementById('console-live-logs-stream');

  // Search Filter
  if (searchInput && tableBody) {
    searchInput.addEventListener('input', () => {
      const query = searchInput.value.toLowerCase();
      const rows = tableBody.querySelectorAll('tr');
      rows.forEach(row => {
        const text = row.textContent.toLowerCase();
        row.style.display = text.includes(query) ? '' : 'none';
      });
    });
  }

  // Cold Wake Trigger Buttons
  document.addEventListener('click', (e) => {
    const wakeBtn = e.target.closest('.run-wake-test');
    if (!wakeBtn) return;

    const appName = wakeBtn.getAttribute('data-app');
    wakeBtn.disabled = true;
    wakeBtn.textContent = 'Unparking...';

    // Find row status badge
    const row = wakeBtn.closest('tr');
    const badge = row ? row.querySelector('.status-badge') : null;

    setTimeout(() => {
      if (badge) {
        badge.className = 'status-badge restored';
        badge.textContent = 'COLD RESTORED';
      }
      wakeBtn.disabled = false;
      wakeBtn.textContent = 'Trigger Wake';

      // Add entry to live log stream
      if (logStream) {
        const now = new Date();
        const timeStr = now.toTimeString().split(' ')[0] + '.' + String(now.getMilliseconds()).padStart(3, '0');
        const logRow = document.createElement('div');
        logRow.className = 'stream-row';
        logRow.innerHTML = `<span class="ts-col">${timeStr}</span><span class="service-col">[vmmd]</span><span class="event-col">FC_RESTORE</span><span class="status-tag ok">184 ms</span><span style="color:#94A3B8;">Snapshot unparked for ${appName} (RAM: 0MB resident when idle)</span>`;
        logStream.prepend(logRow);
      }
    }, 500);
  });
}

/* --------------------------------------------------------------------------
 * 4. Secrets Manager Logic
 * -------------------------------------------------------------------------- */
function initSecretsManager() {
  const secretsTableBody = document.getElementById('secrets-table-body');

  document.addEventListener('click', (e) => {
    // Toggle Mask
    const toggleBtn = e.target.closest('.toggle-secret-val');
    if (toggleBtn) {
      const row = toggleBtn.closest('tr');
      const valCell = row.querySelector('.secret-val-mask');
      if (valCell) {
        if (valCell.textContent.includes('••••')) {
          valCell.textContent = 'sk_live_941a82f0412b5912c41d99';
          valCell.style.color = 'var(--text-primary)';
        } else {
          valCell.textContent = '••••••••••••••••••••••••••••';
          valCell.style.color = 'var(--text-muted)';
        }
      }
    }

    // Delete Secret Row
    const deleteBtn = e.target.closest('.delete-secret-row');
    if (deleteBtn) {
      const row = deleteBtn.closest('tr');
      if (row) row.remove();
    }
  });
}

/* --------------------------------------------------------------------------
 * 5. Modals (New Function & Add Secret)
 * -------------------------------------------------------------------------- */
function initModals() {
  const modalDeploy = document.getElementById('modal-deploy-func');
  const modalSecret = document.getElementById('modal-add-secret');

  const btnDeploy1 = document.getElementById('deploy-new-func-btn');
  const btnDeploy2 = document.getElementById('deploy-new-func-btn-2');
  const btnAddSecret = document.getElementById('add-secret-btn');

  function openModal(modal) {
    if (modal) modal.classList.add('active');
  }

  function closeModal(modal) {
    if (modal) modal.classList.remove('active');
  }

  if (btnDeploy1) btnDeploy1.addEventListener('click', () => openModal(modalDeploy));
  if (btnDeploy2) btnDeploy2.addEventListener('click', () => openModal(modalDeploy));
  if (btnAddSecret) btnAddSecret.addEventListener('click', () => openModal(modalSecret));

  document.querySelectorAll('.close-modal-trigger').forEach(btn => {
    btn.addEventListener('click', () => {
      closeModal(modalDeploy);
      closeModal(modalSecret);
    });
  });

  // Handle Form Submit: Create Function
  const formCreateFunc = document.getElementById('form-create-func');
  if (formCreateFunc) {
    formCreateFunc.addEventListener('submit', (e) => {
      e.preventDefault();
      const funcName = document.getElementById('input-func-name').value.trim();
      const runtime = document.getElementById('input-func-runtime').value;
      const ram = document.getElementById('input-func-ram').value;

      if (!funcName) return;

      const tableBody = document.getElementById('functions-table-body');
      if (tableBody) {
        const tr = document.createElement('tr');
        tr.innerHTML = `
          <td>
            <div class="app-name-cell">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="16 18 22 12 16 6"></polyline><polyline points="8 6 2 12 8 18"></polyline></svg>
              ${funcName}
            </div>
          </td>
          <td>${runtime}</td>
          <td><span class="status-badge restored">COLD RESTORED</span></td>
          <td>${ram}</td>
          <td>165 ms</td>
          <td>hetzner-fsn1</td>
          <td>
            <button class="btn btn-secondary btn-sm run-wake-test" data-app="${funcName}">Trigger Wake</button>
          </td>
        `;
        tableBody.prepend(tr);
      }

      // Update badge count
      const badge = document.getElementById('vms-count-badge');
      if (badge) badge.textContent = parseInt(badge.textContent, 10) + 1;

      closeModal(modalDeploy);
      formCreateFunc.reset();
    });
  }

  // Handle Form Submit: Add Secret
  const formCreateSecret = document.getElementById('form-create-secret');
  if (formCreateSecret) {
    formCreateSecret.addEventListener('submit', (e) => {
      e.preventDefault();
      const keyName = document.getElementById('input-secret-key').value.trim();
      const target = document.getElementById('input-secret-target').value;

      if (!keyName) return;

      const secretsTableBody = document.getElementById('secrets-table-body');
      if (secretsTableBody) {
        const tr = document.createElement('tr');
        tr.innerHTML = `
          <td class="secret-key-code">${keyName}</td>
          <td class="secret-val-mask">••••••••••••••••••••••••••••</td>
          <td>${target}</td>
          <td>
            <button class="btn btn-secondary btn-sm toggle-secret-val">Toggle Mask</button>
            <button class="btn btn-secondary btn-sm delete-secret-row" style="color: #EF4444;">Delete</button>
          </td>
        `;
        secretsTableBody.prepend(tr);
      }

      closeModal(modalSecret);
      formCreateSecret.reset();
    });
  }
}

/* --------------------------------------------------------------------------
 * 6. Multi-Language Code Showcase Tabs Switcher
 * -------------------------------------------------------------------------- */
function initCodeShowcaseTabs() {
  const tabBtns = document.querySelectorAll('.code-tab-btn');
  const snippets = document.querySelectorAll('.code-snippet');
  const copyCodeBtn = document.getElementById('copy-active-code');

  if (!tabBtns.length || !snippets.length) return;

  tabBtns.forEach(btn => {
    btn.addEventListener('click', () => {
      const targetTab = btn.getAttribute('data-tab');

      tabBtns.forEach(b => b.classList.remove('active'));
      btn.classList.add('active');

      snippets.forEach(snippet => {
        if (snippet.id === `tab-${targetTab}`) {
          snippet.classList.add('active');
        } else {
          snippet.classList.remove('active');
        }
      });
    });
  });

  if (copyCodeBtn) {
    copyCodeBtn.addEventListener('click', () => {
      const activeSnippet = document.querySelector('.code-snippet.active');
      if (!activeSnippet) return;

      const codeText = activeSnippet.textContent;
      navigator.clipboard.writeText(codeText).then(() => {
        const originalText = copyCodeBtn.textContent;
        copyCodeBtn.textContent = 'Copied!';
        copyCodeBtn.style.color = '#15803D';

        setTimeout(() => {
          copyCodeBtn.textContent = originalText;
          copyCodeBtn.style.color = '';
        }, 1800);
      }).catch(err => {
        console.error('Failed to copy code: ', err);
      });
    });
  }
}

/* --------------------------------------------------------------------------
 * 7. Benchmark Trigger
 * -------------------------------------------------------------------------- */
function initBenchmarkCard() {
  const runSimBtn = document.getElementById('run-sim-btn');
  const benchBarGregale = document.getElementById('bench-bar-gregale');
  const simGregaleMs = document.getElementById('sim-gregale-ms');

  if (!runSimBtn || !benchBarGregale) return;

  let isRunning = false;

  runSimBtn.addEventListener('click', () => {
    if (isRunning) return;
    isRunning = true;

    runSimBtn.disabled = true;
    runSimBtn.textContent = 'Simulating...';

    benchBarGregale.style.width = '0%';
    simGregaleMs.textContent = '0 ms';

    setTimeout(() => {
      benchBarGregale.style.width = '15%';
      simGregaleMs.textContent = '310 ms';
      runSimBtn.disabled = false;
      runSimBtn.textContent = 'Simulate Request Trigger';
      isRunning = false;
    }, 600);
  });
}

/* --------------------------------------------------------------------------
 * 8. Pricing Calculator
 * -------------------------------------------------------------------------- */
function initPricingCalculator() {
  const ramInput = document.getElementById('calc-ram');
  const hoursInput = document.getElementById('calc-hours');

  const ramValDisplay = document.getElementById('calc-ram-val');
  const hoursValDisplay = document.getElementById('calc-hours-val');
  const gbhValDisplay = document.getElementById('calc-gbh-val');
  const costEstimateDisplay = document.getElementById('calc-cost-val');
  const recommendedPlanDisplay = document.getElementById('calc-plan-val');

  if (!ramInput || !hoursInput) return;

  function updateCalculator() {
    const ramMB = parseInt(ramInput.value, 10);
    const hoursMonth = parseInt(hoursInput.value, 10);

    const ramGB = ramMB / 1024;
    const gbHours = Math.round(ramGB * hoursMonth);

    ramValDisplay.textContent = `${ramMB} MB`;
    hoursValDisplay.textContent = `${hoursMonth.toLocaleString()} hrs/mo`;
    gbhValDisplay.textContent = `${gbHours.toLocaleString()} GB-h`;

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

    costEstimateDisplay.textContent = `€${totalCost.toFixed(2)}`;
    recommendedPlanDisplay.textContent = `${recommendedPlan} Plan`;
  }

  ramInput.addEventListener('input', updateCalculator);
  hoursInput.addEventListener('input', updateCalculator);

  updateCalculator();
}

/* --------------------------------------------------------------------------
 * 9. Copy Helper
 * -------------------------------------------------------------------------- */
function initCopyButtons() {
  const copyBtns = document.querySelectorAll('.copy-btn');

  copyBtns.forEach(btn => {
    if (btn.id === 'copy-active-code') return;

    btn.addEventListener('click', () => {
      const textToCopy = btn.getAttribute('data-copy');
      if (!textToCopy) return;

      navigator.clipboard.writeText(textToCopy).then(() => {
        const originalText = btn.textContent;
        btn.textContent = 'Copied';
        btn.style.color = '#15803D';

        setTimeout(() => {
          btn.textContent = originalText;
          btn.style.color = '';
        }, 1800);
      }).catch(err => {
        console.error('Failed to copy: ', err);
      });
    });
  });
}
