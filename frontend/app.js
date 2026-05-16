// ── Configuration ───────────────────────────────────────────────
const API = window.location.origin.includes('5173')
  ? 'http://localhost:8080'  // dev: frontend served separately
  : '';                       // prod: same origin

// ── Router ──────────────────────────────────────────────────────
const pages = document.querySelectorAll('.page');
const navLinks = document.querySelectorAll('.nav-link');

function showPage(name) {
  pages.forEach(p => p.classList.toggle('active', p.id === `page-${name}`));
  navLinks.forEach(l => l.classList.toggle('active', l.dataset.page === name));
  if (name === 'dashboard') loadDashboard();
  if (name === 'leaderboard') loadLeaderboard();
}

navLinks.forEach(link => {
  link.addEventListener('click', e => {
    e.preventDefault();
    showPage(link.dataset.page);
  });
});

// ── API Helpers ─────────────────────────────────────────────────
async function api(path, opts = {}) {
  const res = await fetch(`${API}${path}`, opts);
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body?.error?.message || `HTTP ${res.status}`);
  }
  if (res.status === 204) return null;
  return res.json();
}

function badge(status) {
  return `<span class="badge badge-${status}">${status}</span>`;
}

function timeAgo(ts) {
  const d = new Date(ts);
  const s = Math.floor((Date.now() - d) / 1000);
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return d.toLocaleDateString();
}

// ── Health Check ────────────────────────────────────────────────
async function checkHealth() {
  const el = document.getElementById('health-status');
  try {
    const r = await api('/health');
    const status = r?.data?.status || 'unknown';
    el.textContent = status === 'ok' ? '● Healthy' : '⚠ Degraded';
    el.className = 'nav-status ' + (status === 'ok' ? 'ok' : 'down');
  } catch {
    el.textContent = '● Offline';
    el.className = 'nav-status down';
  }
}

// ── Dashboard ───────────────────────────────────────────────────
async function loadDashboard() {
  try {
    const r = await api('/api/v1/submissions?page_size=10');
    const subs = r?.data || [];
    const total = r?.meta?.total_items || subs.length;

    document.getElementById('stat-submissions').textContent = total;
    document.getElementById('stat-active-runs').textContent =
      subs.filter(s => s.status === 'running' || s.status === 'building').length;
    document.getElementById('stat-completed').textContent =
      subs.filter(s => s.status === 'completed').length;

    const tbody = document.getElementById('recent-submissions-body');
    if (subs.length === 0) {
      tbody.innerHTML = '<tr><td colspan="5" class="empty">No submissions yet</td></tr>';
      return;
    }

    tbody.innerHTML = subs.map(s => `
      <tr>
        <td>${esc(s.team_name)}</td>
        <td>${esc(s.language)}</td>
        <td>${badge(s.status)}</td>
        <td>${timeAgo(s.created_at)}</td>
        <td>
          <button class="btn-sm primary" onclick="triggerRun('${s.id}')"
                  ${['pending','ready','completed'].includes(s.status) ? '' : 'disabled'}>
            Run
          </button>
          <button class="btn-sm danger" onclick="deleteSubmission('${s.id}', '${esc(s.team_name)}')"
                  ${s.status === 'running' ? 'disabled' : ''}>
            Delete
          </button>
        </td>
      </tr>
    `).join('');
  } catch (err) {
    document.getElementById('recent-submissions-body').innerHTML =
      `<tr><td colspan="5" class="empty">Error: ${esc(err.message)}</td></tr>`;
  }
}

// ── Trigger Benchmark Run ───────────────────────────────────────
async function triggerRun(subId) {
  try {
    await api(`/api/v1/submissions/${subId}/run`, { method: 'POST' });
    loadDashboard();
  } catch (err) {
    alert('Failed to trigger run: ' + err.message);
  }
}

// ── Delete Submission ───────────────────────────────────────────
async function deleteSubmission(subId, teamName) {
  if (!confirm(`Delete submission for "${teamName}"? This cannot be undone.`)) return;
  try {
    await api(`/api/v1/submissions/${subId}`, { method: 'DELETE' });
    loadDashboard();
  } catch (err) {
    alert('Failed to delete: ' + err.message);
  }
}

// ── Submission Form ─────────────────────────────────────────────
document.getElementById('submission-form').addEventListener('submit', async e => {
  e.preventDefault();
  const btn = document.getElementById('submit-btn');
  const result = document.getElementById('submit-result');
  btn.disabled = true;
  btn.textContent = 'Uploading...';
  result.style.display = 'none';

  try {
    const form = new FormData(e.target);
    const r = await api('/api/v1/submissions', { method: 'POST', body: form });
    const sub = r?.data;
    result.className = 'result-box success';
    result.innerHTML = `✓ Submission <strong>${esc(sub?.id?.slice(0,8))}</strong> created for team <strong>${esc(sub?.team_name)}</strong>`;
    result.style.display = 'block';
    e.target.reset();
  } catch (err) {
    result.className = 'result-box error';
    result.textContent = '✗ ' + err.message;
    result.style.display = 'block';
  } finally {
    btn.disabled = false;
    btn.textContent = 'Upload Submission';
  }
});

// ── Leaderboard ─────────────────────────────────────────────────
async function loadLeaderboard() {
  try {
    const r = await api('/api/v1/leaderboard');
    const snap = r?.data;
    const entries = snap?.entries || [];

    document.getElementById('leaderboard-updated').textContent =
      `Last updated: ${snap?.generated_at ? new Date(snap.generated_at).toLocaleTimeString() : '—'}`;

    const tbody = document.getElementById('leaderboard-body');
    if (entries.length === 0) {
      tbody.innerHTML = '<tr><td colspan="8" class="empty">No scored submissions yet</td></tr>';
      return;
    }

    tbody.innerHTML = entries.map(e => `
      <tr onclick="loadRunMetrics('${e.run_id}', this)">
        <td><strong>#${e.rank}</strong></td>
        <td>${esc(e.team_name)}</td>
        <td>${esc(e.language)}</td>
        <td>${fmt(e.peak_tps, 0)}</td>
        <td>${fmt(e.avg_p99_ms, 2)} ms</td>
        <td>${fmt(e.correctness * 100, 1)}%</td>
        <td>${fmt(e.uptime_pct, 1)}%</td>
        <td><strong>${fmt(e.composite_score, 1)}</strong></td>
      </tr>
    `).join('');
  } catch (err) {
    document.getElementById('leaderboard-body').innerHTML =
      `<tr><td colspan="8" class="empty">Error: ${esc(err.message)}</td></tr>`;
  }
}

// ── Run Metrics ─────────────────────────────────────────────────
async function loadRunMetrics(runId, row) {
  // Highlight selected row
  document.querySelectorAll('#leaderboard-table tr.selected').forEach(r => r.classList.remove('selected'));
  if (row) row.classList.add('selected');

  const table = document.getElementById('metrics-table');
  const tbody = document.getElementById('metrics-body');

  try {
    const r = await api(`/api/v1/leaderboard/runs/${runId}/metrics?limit=50`);
    const snapshots = r?.data || [];

    if (snapshots.length === 0) {
      tbody.innerHTML = '<tr><td colspan="8" class="empty">No metrics available</td></tr>';
    } else {
      tbody.innerHTML = snapshots.map(s => `
        <tr>
          <td>${new Date(s.recorded_at).toLocaleTimeString()}</td>
          <td>${fmt(s.tps, 0)}</td>
          <td>${fmt(s.p50_ms, 2)}</td>
          <td>${fmt(s.p90_ms, 2)}</td>
          <td>${fmt(s.p99_ms, 2)}</td>
          <td>${s.orders_sent}</td>
          <td>${s.orders_acked}</td>
          <td>${s.fill_errors + s.priority_errors}</td>
        </tr>
      `).join('');
    }
    table.style.display = 'table';
  } catch (err) {
    tbody.innerHTML = `<tr><td colspan="8" class="empty">Error: ${esc(err.message)}</td></tr>`;
    table.style.display = 'table';
  }
}

// ── Utilities ───────────────────────────────────────────────────
function esc(s) {
  if (!s) return '';
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

function fmt(n, decimals) {
  if (n == null || isNaN(n)) return '—';
  return Number(n).toFixed(decimals);
}

// ── Init ────────────────────────────────────────────────────────
checkHealth();
setInterval(checkHealth, 30000);
loadDashboard();
