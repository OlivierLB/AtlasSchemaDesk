// Atlas Schema Desk — One-click export, schema versions, S3 dumps and dump restore.
// Classic script: shares the global scope with the other public/js files, loaded in order by index.html.
let exporting = false;

// ---- Per-database version history (written by scripts/snapshot.py) ----

let latestSources = {};
let selectedVersions = {}; // engine -> snapshot hash; absent = latest export
let versionIndex = null;

const formatDate = (iso) => new Date(iso).toLocaleString('fr-FR', { dateStyle: 'short', timeStyle: 'short' });

function updateEngineSummary() {
  const part = (engine, label) => `${label}${selectedVersions[engine] ? ' ⏱' : ''} · ${(sources[engine] || []).length}`;
  $('#engine-summary').textContent = ENGINES.map((id) => part(id, engineLabel(id))).join('   ');
}

function latestVersionHash(engine) {
  const versions = versionIndex?.versions || [];
  return versions.length ? versions[versions.length - 1].sources?.[engine]?.hash ?? null : null;
}

// Distinct schema versions of one database, most recently seen first.
function sourceVersions(engine) {
  const byHash = new Map();
  for (const entry of versionIndex?.versions || []) {
    const source = entry.sources?.[engine];
    if (!source) continue;
    const known = byHash.get(source.hash);
    if (known) known.lastSeen = entry.generatedAt;
    else byHash.set(source.hash, { hash: source.hash, tables: source.tables, firstSeen: entry.generatedAt, lastSeen: entry.generatedAt, origin: entry.origin });
  }
  return [...byHash.values()].sort((a, b) => b.lastSeen.localeCompare(a.lastSeen));
}

async function loadVersionIndex() {
  try {
    const response = await fetch(`data/snapshots/index.json?t=${Date.now()}`, { cache: 'no-store' });
    versionIndex = response.ok ? await response.json() : null;
  } catch (error) {
    console.warn('[Atlas Schema Desk] Historique des versions illisible', error);
    versionIndex = null;
  }
  await loadS3Dumps();
}

// ---- Dated dumps available on S3 (written by scripts/s3-dumps.py, refreshed by the server) ----
let s3Dumps = null;
let s3Checking = false;

let appliedDumps = {}; // engine -> {versionId, lastModified, appliedAt} (written by apply-dump-version.sh)
let applyJob = null; // last /api/status "apply" payload while a restore runs
let sudoCommand = null; // {engine, command} shown when a restore must run in a terminal

async function fetchJson(url) {
  const response = await fetch(`${url}?t=${Date.now()}`, { cache: 'no-store' });
  return response.ok ? response.json() : null;
}

async function loadS3Dumps() {
  if (!appConfig.modules.dumps) { s3Dumps = null; renderVersionPanel(); return; }
  try {
    [s3Dumps, appliedDumps] = await Promise.all([fetchJson('data/s3-dumps.json'), fetchJson('data/applied-dumps.json').then((data) => data || {})]);
  } catch (error) {
    console.warn('[Atlas Schema Desk] Liste des dumps S3 illisible', error);
    s3Dumps = null;
  }
  renderVersionPanel();
}

// Restore one dated dump: PostgreSQL bases run on the server; sudo-protected ones show a terminal command.
async function applyDumpVersion(engine, versionId) {
  const target = s3Dumps?.targets?.find((item) => item.engine === engine);
  const version = target?.versions?.find((item) => item.versionId === versionId);
  if (!target || !version) return;
  if (target.needsSudo) {
    sudoCommand = { engine, command: `${s3Dumps.applyScript} ${engine} ${versionId}` };
    renderVersionPanel();
    return;
  }
  const answer = window.prompt(`Remplacer la base locale « ${target.label} » par le dump du ${formatDate(version.lastModified)} (${formatSize(version.size)}) ?\n\nLes données locales actuelles seront perdues. Le téléchargement puis la restauration peuvent prendre plusieurs minutes.\n\nTape « ${engine} » pour confirmer :`);
  if (answer !== engine) return;
  try {
    const response = await fetch('api/apply', { method: 'POST', headers: { 'X-DB-Atlas': '1', 'Content-Type': 'application/json' }, body: JSON.stringify({ engine, versionId }) });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(payload.error || `HTTP ${response.status}`);
    await followApplyJob();
  } catch (error) {
    showLoadBanner(`Restauration impossible : ${error.message}`);
  }
}

async function followApplyJob() {
  let status;
  do {
    status = (await fetchExportStatus()).apply;
    applyJob = status;
    const lastLine = (status?.log || '').trim().split('\n').pop() || 'démarrage…';
    updateStatus(`Restauration ${engineLabel(status?.context?.engine)} : ${lastLine}`, 'loading');
    renderVersionPanel();
    if (status?.state === 'running') await new Promise((resolve) => setTimeout(resolve, 2000));
  } while (status?.state === 'running');
  applyJob = null;
  if (status?.state === 'error') {
    const lastLines = (status.log || '').trim().split('\n').slice(-3).join(' · ');
    showLoadBanner(`Restauration en échec (code ${status.exitCode}) : ${lastLines}`);
    updateStatus('Restauration en échec', 'error');
  }
  await loadSchema(); // the apply script re-exported the schema
}

async function resumeApplyIfRunning() {
  try {
    if ((await fetchExportStatus()).apply?.state === 'running') await followApplyJob();
  } catch { /* older server without /api/status apply: nothing to resume */ }
}

const formatSize = (bytes) => (Number.isFinite(bytes) ? `${(bytes / 1024 ** 3).toFixed(1).replace('.', ',')} Go` : '?');

function s3Markup(engine) {
  if (!appConfig.modules.dumps) return "";
  const target = s3Dumps?.targets?.find((item) => item.engine === engine);
  if (!target) return '';
  const versions = target.versions || [];
  const error = target.error ? `<p class="s3-line error">⚠ ${esc(target.error.message)}${target.checkedAt ? ` — liste du ${esc(formatDate(target.checkedAt))} conservée` : ''}</p>` : '';
  if (!versions.length) return `${error}<p class="s3-line muted">Dumps S3 : aucune version trouvée pour ${esc(target.key)}.</p>`;
  const versioning = target.versioning === false ? '<p class="s3-line muted">Versioning S3 inactif : seul le dernier dump existe.</p>' : '';
  const applied = appliedDumps[engine];
  const busy = Boolean(applyJob);
  const items = versions.map((version) => {
    const isApplied = applied?.versionId === version.versionId;
    const localTag = isApplied
      ? `<span class="s3-tag local" title="Appliquée le ${esc(formatDate(applied.appliedAt))}">appliquée</span>`
      : (!applied && version.matchesLocalDump ? '<span class="s3-tag local" title="Même taille que le dump présent en local">en local</span>' : '');
    return `<li><span>${esc(formatDate(version.lastModified))}</span><span>${formatSize(version.size)}</span>${version.isLatest ? '<span class="s3-tag">dernier</span>' : ''}${localTag}<button class="s3-apply" type="button" data-apply-engine="${engine}" data-apply-version="${esc(version.versionId)}"${busy ? ' disabled' : ''} title="Télécharger ce dump et restaurer la base locale">Appliquer</button></li>`;
  }).join('');
  const sudo = sudoCommand?.engine === engine
    ? `<div class="s3-sudo"><p class="s3-line">Cette base demande <code>sudo</code> : lance dans un terminal (téléchargement, restauration puis réexport automatiques) :</p><code class="s3-command">${esc(sudoCommand.command)}</code><button class="button" type="button" id="s3-copy">Copier</button></div>`
    : '';
  const running = applyJob?.context?.engine === engine
    ? `<p class="s3-line running">⏳ Restauration en cours : ${esc((applyJob.log || '').trim().split('\n').pop() || 'démarrage…')}</p>`
    : '';
  return `${error}${running}${sudo}<details class="s3-dumps"${sudo || running ? ' open' : ''}><summary>Dumps S3 · ${versions.length} · dernier le ${esc(formatDate(versions[0].lastModified))}</summary>${versioning}<ul>${items}</ul></details>`;
}

// Ask the server to re-list S3 now, then reload the list.
async function refreshS3Dumps() {
  if (s3Checking) return;
  s3Checking = true;
  renderVersionPanel();
  try {
    const response = await fetch('api/s3/refresh', { method: 'POST', headers: { 'X-DB-Atlas': '1' } });
    if (!response.ok && response.status !== 409) throw new Error(`HTTP ${response.status}`);
    let status;
    do {
      await new Promise((resolve) => setTimeout(resolve, 1000));
      status = (await fetchExportStatus()).s3;
    } while (status?.state === 'running');
  } catch (error) {
    console.error('[Atlas Schema Desk] Vérification S3 impossible', error);
    showLoadBanner(`Vérification S3 impossible : ${error.message}. Redémarre Atlas Schema Desk si le serveur est ancien.`);
  } finally {
    s3Checking = false;
    await loadS3Dumps();
  }
}

function renderVersionPanel() {
  const panel = $('#versions-panel');
  const pastCount = Object.keys(selectedVersions).length;
  $('#versions-toggle').classList.toggle('is-past', pastCount > 0);
  $('#versions-text').textContent = pastCount ? `Versions · ${pastCount} ancienne${pastCount > 1 ? 's' : ''}` : 'Versions';
  if (!versionIndex?.versions?.length && !s3Dumps) {
    panel.innerHTML = '<p class="versions-empty">Aucune version enregistrée. Chaque export (bouton Actualiser) en ajoute une.</p>';
    return;
  }
  const rows = ENGINES.map((engine) => {
    const versions = sourceVersions(engine);
    const latest = latestVersionHash(engine);
    const current = selectedVersions[engine] || latest;
    const options = versions.map((version) => {
      const period = version.firstSeen === version.lastSeen ? formatDate(version.firstSeen) : `${formatDate(version.firstSeen)} → ${formatDate(version.lastSeen)}`;
      const tag = version.hash === latest ? ' · actuelle' : '';
      return `<option value="${esc(version.hash)}"${version.hash === current ? ' selected' : ''}>${esc(period)} · ${version.tables} tables${tag}</option>`;
    }).join('');
    const note = versions.length <= 1 ? '<span class="versions-note">Une seule version enregistrée pour l’instant</span>' : '';
    return `<div class="version-row"><label class="version-row"><span class="version-engine ${engine}">${esc(engineLabel(engine))}</span>
      <select data-version-engine="${engine}"${versions.length <= 1 ? ' disabled' : ''}>${options}</select>${note}</label>${s3Markup(engine)}</div>`;
  }).join('');
  const reset = pastCount ? '<button class="button versions-reset" type="button" id="versions-reset">Revenir aux versions actuelles</button>' : '';
  const s3Checked = s3Dumps?.generatedAt ? `S3 vérifié le ${esc(formatDate(s3Dumps.generatedAt))}` : 'S3 jamais vérifié';
  const s3Footer = `<div class="s3-footer"><span>${s3Checked} · vérifié à chaque démarrage de Atlas Schema Desk</span><button class="button" type="button" id="s3-refresh"${s3Checking ? ' disabled' : ''}>${s3Checking ? 'Vérification…' : 'Vérifier S3'}</button></div>`;
  panel.innerHTML = `<p class="versions-help">Affiche le schéma d’une base tel qu’il était lors d’un export passé. Lecture seule : les bases ne sont pas modifiées.</p>${rows}${reset}${appConfig.modules.dumps ? s3Footer : ""}`;
  $('#s3-refresh')?.addEventListener('click', refreshS3Dumps);
  panel.querySelectorAll('[data-apply-engine]').forEach((button) => button.addEventListener('click', () => applyDumpVersion(button.dataset.applyEngine, button.dataset.applyVersion)));
  $('#s3-copy')?.addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(sudoCommand.command);
      $('#s3-copy').textContent = 'Copié';
    } catch {
      $('#s3-copy').textContent = 'Copie impossible : sélectionne la commande';
    }
  });
  panel.querySelectorAll('[data-version-engine]').forEach((select) => select.addEventListener('change', () => selectSourceVersion(select.dataset.versionEngine, select.value)));
  $('#versions-reset')?.addEventListener('click', async () => {
    for (const engine of Object.keys(selectedVersions)) await selectSourceVersion(engine, null);
  });
}

async function selectSourceVersion(engine, hash) {
  try {
    if (!hash || hash === latestVersionHash(engine)) {
      delete selectedVersions[engine];
      sources[engine] = latestSources[engine] || [];
    } else {
      const response = await fetch(`data/snapshots/${encodeURIComponent(engine)}/${encodeURIComponent(hash)}.json`, { cache: 'no-store' });
      if (!response.ok) throw new Error(`version ${hash} introuvable (HTTP ${response.status})`);
      sources[engine] = await response.json();
      selectedVersions[engine] = hash;
    }
    all = allTables();
    relationCache = null;
    dataView = null; // rows belonged to the previous version's table definition
    render(); // a table absent from this version shows « Introuvable »
    updateEngineSummary();
    renderVersionPanel();
    const past = Object.keys(selectedVersions).map((name) => engineLabel(name)).join(', ');
    updateStatus(past ? `Version passée affichée : ${past}` : `${all.length} tables · versions actuelles`, past ? 'loading' : '');
  } catch (error) {
    console.error('[Atlas Schema Desk] Changement de version impossible', error);
    showLoadBanner(`Changement de version impossible pour ${engineLabel(engine)} : ${error.message}`);
    renderVersionPanel();
  }
}

function toggleVersionPanel(open) {
  const panel = $('#versions-panel');
  const next = open ?? panel.hidden;
  panel.hidden = !next;
  $('#versions-toggle').setAttribute('aria-expanded', String(next));
}

function showLoadBanner(message) {
  $('#load-banner').textContent = message;
  $('#load-banner').classList.add('visible');
}

async function fetchExportStatus() {
  const response = await fetch('api/status', { cache: 'no-store' });
  if (!response.ok) throw new Error(`statut HTTP ${response.status}`);
  return response.json();
}

// Runs a real export on the server (databases → schema.json), then reloads the UI.
async function refreshExport() {
  if (exporting || loading) return;
  exporting = true;
  const button = $('#refresh');
  const label = $('#refresh-text');
  button.disabled = true;
  label.textContent = 'Export…';
  $('#load-banner').classList.remove('visible');
  const startedAt = Date.now();
  try {
    const response = await fetch('api/refresh', { method: 'POST', headers: { 'X-DB-Atlas': '1' } });
    if (response.status === 404 || response.status === 501) {
      throw new Error('Ce serveur ne sait pas lancer l’export. Redémarre Atlas Schema Desk avec « atlas-schema-desk ».');
    }
    if (!response.ok && response.status !== 409) {
      const payload = await response.json().catch(() => ({}));
      throw new Error(payload.error || `Lancement de l’export refusé (HTTP ${response.status})`);
    }
    let status;
    do {
      const elapsed = Math.round((Date.now() - startedAt) / 1000);
      updateStatus(`Export des bases configurées… ${elapsed} s`, 'loading');
      await new Promise((resolve) => setTimeout(resolve, 1000));
      status = await fetchExportStatus();
    } while (status.state === 'running');
    if (status.state !== 'success') {
      const lastLines = (status.log || '').trim().split('\n').slice(-4).join(' · ');
      throw new Error(`Export en échec (code ${status.exitCode ?? '?'}) : ${lastLines || 'aucun détail'}`);
    }
    exporting = false;
    await loadSchema();
    updateStatus(`Export à jour (${status.durationSeconds} s) · ${all.length} tables`);
  } catch (error) {
    console.error('[Atlas Schema Desk] Actualisation impossible', error);
    updateStatus('Actualisation impossible', 'error');
    showLoadBanner(error.message);
  } finally {
    exporting = false;
    button.disabled = false;
    label.textContent = 'Actualiser';
  }
}
