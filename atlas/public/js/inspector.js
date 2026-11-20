// Atlas Schema Desk — Inspector: SQL program sheet and row sheet (the table itself has its own page).
// Classic script: shares the global scope with the other public/js files, loaded in order by index.html.

// The inspector column only exists while a sheet (program or row) is open.
function setInspectorCollapsed(collapsed) {
  const workspace = $('.workspace');
  if (workspace.classList.contains('no-selection') === collapsed) return;
  workspace.classList.toggle('no-selection', collapsed);
  window.dispatchEvent(new Event('resize')); // the content width changed (neighborhood lines, grid)
}

// ---- Stored SQL programs (public/data/programs.json, written by scripts/export-programs.py) ----
let programsData = null; // { generatedAt, engines: { engine: { programs, tableImpacts, error } } }
let programsById = new Map();
let selectedProgramId = null;

const PROGRAM_KINDS = {
  function: 'Fonction', procedure: 'Procédure', trigger: 'Trigger', trigger_function: 'Fonction de trigger',
  view: 'Vue', materialized_view: 'Vue matérialisée', event: 'Event planifié',
};
const programEngine = (id) => id.split(':', 1)[0];

async function loadPrograms() {
  try {
    programsData = await fetchJson('data/programs.json');
  } catch (error) {
    console.warn('[Atlas Schema Desk] Programmes SQL illisibles', error);
    programsData = null;
  }
  programsById = new Map();
  for (const engine of Object.values(programsData?.engines || {})) {
    for (const program of engine.programs || []) programsById.set(program.id, program);
  }
  const errors = Object.entries(programsData?.engines || {}).filter(([, engine]) => engine.error).map(([name, engine]) => `${engineLabel(name)} : ${engine.error.message}`);
  if (errors.length) showLoadBanner(`Programmes SQL incomplets — ${errors.join(' · ')}`);
  render(); // tables unchanged: refreshes the trigger badges and the inspector
}

function tableImpact(table) {
  return programsData?.engines?.[table.engine]?.tableImpacts?.[`${table.schema}.${table.name}`] || null;
}

// Catalog estimates (scripts/export-programs.py): rows and on-disk size, no data read.
function tableStats(table) {
  return programsData?.engines?.[table.engine]?.tableStats?.[`${table.schema}.${table.name}`] || null;
}

function formatRows(rows, long = false) {
  if (long) return `${rows.toLocaleString('fr-FR')} ligne${rows > 1 ? 's' : ''}`;
  if (rows >= 1e6) return `${(rows / 1e6).toFixed(rows >= 1e7 ? 0 : 1).replace('.', ',')} M`;
  if (rows >= 1e3) return `${Math.round(rows / 1e3)} k`;
  return String(rows);
}

function formatBytes(bytes) {
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(1).replace('.', ',')} Go`;
  if (bytes >= 1024 ** 2) return `${Math.round(bytes / 1024 ** 2)} Mo`;
  return `${Math.max(1, Math.round(bytes / 1024))} Ko`;
}

function statsLine(table) {
  const stats = tableStats(table);
  if (stats && stats.rows == null && stats.bytes) return ` · <span title="Statistiques absentes (table jamais analysée) : seule la taille est connue">${formatBytes(stats.bytes)}, nombre de lignes inconnu</span>`;
  if (!stats || stats.rows == null) return '';
  return ` · <span title="Estimation du catalogue (statistiques), sans lecture des données">≈ ${formatRows(stats.rows, true)}${stats.bytes != null ? ` · ${formatBytes(stats.bytes)}` : ''}</span>`;
}

const SOURCE_STATUS = {
  same: ['identique au dépôt', 'source-same', 'Le code en base correspond au fichier (espaces, commentaires et guillemets ignorés).'],
  different: ['diffère du dépôt', 'source-different', 'Le code en base ne correspond pas au fichier : routine modifiée en base, ou dépôt plus récent que la base. Peut aussi venir d’un texte mal encodé en base.'],
  absent: ['absent du dépôt routines repository', 'source-absent', 'Aucun CREATE pour ce programme dans the configured routines directory.'],
  not_comparable: ['dans le dépôt (vue : non comparée)', 'source-same', 'MySQL réécrit le texte des vues : comparaison impossible.'],
};

function sourceMarkup(source) {
  const [label, css, help] = SOURCE_STATUS[source.status] || [source.status, '', ''];
  const where = source.path ? `<code>${esc(source.path)}</code> ligne ${source.line} <button class="link-button" type="button" data-copy="${esc(source.absolutePath)}">copier le chemin</button> · ` : '';
  return `Sources : ${where}<span class="source-status ${css}" title="${esc(help)}">${esc(label)}</span>`;
}

function openProgram(id) {
  if (!programsById.has(id)) return;
  selectedProgramId = id;
  renderDetail(); // only the inspector: the page (and a loaded data grid) stays as is
}

function programButton(id, extra = '') {
  const program = programsById.get(id);
  if (!program) return `<button class="reference-button" type="button" disabled><span class="reference-copy">${esc(id)}</span></button>`;
  const flag = program.impact?.dynamicSql ? ' <span class="program-flag" title="SQL dynamique : impact partiellement déduit">dyn.</span>' : '';
  return `<button class="reference-button program-ref" type="button" data-program-id="${esc(id)}"><span class="reference-copy"><span class="program-kind">${esc(PROGRAM_KINDS[program.kind] || program.kind)}</span> ${esc(program.schema)}.${esc(program.name)}${extra ? ` <span class="program-extra">${esc(extra)}</span>` : ''}${flag}</span><span class="reference-arrow">↗</span></button>`;
}

function relationButton(engine, qualifiedName) {
  const dot = qualifiedName.indexOf('.');
  const schema = qualifiedName.slice(0, dot);
  const name = qualifiedName.slice(dot + 1);
  const table = findTable(engine, schema, name);
  if (table) return `<button class="reference-button" type="button" data-key="${esc(encodeURIComponent(keyOf(table)))}"><span class="reference-copy">${esc(qualifiedName)}</span><span class="reference-arrow">↗</span></button>`;
  const viewId = [`${engine}:view:${qualifiedName}`, `${engine}:materialized_view:${qualifiedName}`].find((id) => programsById.has(id));
  return viewId ? programButton(viewId) : `<button class="reference-button" type="button" disabled><span class="reference-copy">${esc(qualifiedName)}</span></button>`;
}

// Collapsible list section; long lists stay closed so the inspector remains readable.
function listSection(title, items, { open = items.length <= 8, note = '' } = {}) {
  if (!items.length) return '';
  return `<details class="detail-section program-section"${open ? ' open' : ''}><summary class="detail-section-heading"><span>${title}</span><span>${items.length}</span></summary>${note ? `<p class="program-note">${note}</p>` : ''}<div class="reference-list">${items.join('')}</div></details>`;
}

function tableProgramsMarkup(table) {
  if (!programsData) return '';
  const impact = tableImpact(table);
  if (!impact) return '<section class="detail-section"><div class="detail-section-heading"><span>Programmes SQL</span><span>0</span></div><p class="program-note">Aucun trigger, fonction, procédure, vue ou event ne référence cette table.</p></section>';
  const triggers = impact.triggers.map((id) => {
    const program = programsById.get(id);
    return programButton(id, program ? `${program.timing || ''} ${(program.events || []).join('/')}`.trim() : '');
  });
  const dynamicNote = 'Déduit du texte de requêtes construites dynamiquement (PREPARE/EXECUTE) : à confirmer dans le code.';
  return `<div class="program-block-title">Programmes SQL</div>
    ${listSection('⚡ Triggers sur cette table', triggers, { open: true })}
    ${listSection('Écrite par', impact.writes.map((id) => programButton(id)))}
    ${listSection('Écrite indirectement (via appels)', impact.indirectWrites.map((id) => programButton(id)), { open: false, note: 'Programmes qui appellent, directement ou en chaîne, un programme qui écrit cette table.' })}
    ${listSection('Probablement écrite (SQL dynamique)', impact.probableWrites.map((id) => programButton(id)), { open: false, note: dynamicNote })}
    ${listSection('Lue par', impact.reads.map((id) => programButton(id)), { open: false })}
    ${listSection('Probablement lue (SQL dynamique)', impact.probableReads.map((id) => programButton(id)), { open: false, note: dynamicNote })}`;
}

function renderProgramDetail(program) {
  const detail = $('#detail');
  const engine = programEngine(program.id);
  const impact = program.impact || {};
  const facts = [];
  if (program.signature) facts.push(`<code>${esc(program.signature)}</code>`);
  if (program.returns) facts.push(`retourne <code>${esc(program.returns)}</code>`);
  if (program.language) facts.push(`langage ${esc(program.language)}`);
  if (program.kind === 'trigger') facts.push(`${esc(program.timing || '')} ${esc((program.events || []).join(' / '))} sur ${relationButton(engine, `${program.table.schema}.${program.table.name}`)}`);
  if (program.kind === 'event') facts.push(`${esc(program.schedule)} · ${program.status === 'ENABLED' ? 'actif' : esc(program.status)}${program.lastExecuted ? ` · dernière exécution ${esc(program.lastExecuted)}` : ''}`);
  if (program.lastAltered) facts.push(`modifié le ${esc(program.lastAltered)}`);
  if (program.source) facts.push(sourceMarkup(program.source));
  if (program.comment) facts.push(esc(program.comment));
  const dynamic = impact.dynamicSql ? '<p class="program-warning">SQL dynamique (PREPARE/EXECUTE) : les tables construites dans des chaînes sont listées comme « probables » et peuvent être incomplètes.</p>' : '';
  const tables = (names) => (names || []).map((name) => relationButton(engine, name));
  const programs = (ids) => (ids || []).map((id) => programButton(id));
  const empty = !['writes', 'reads', 'calls', 'probableWrites', 'probableReads', 'indirectWrites', 'calledBy'].some((key) => (impact[key] || []).length);
  detail.innerHTML = `<section class="detail-main"><div class="detail-top"><div><div class="detail-engine">${esc(engineLabel(engine))} · ${esc(PROGRAM_KINDS[program.kind] || program.kind)} · ${esc(program.schema)}</div><h3 class="detail-title">${esc(program.name)}</h3></div><button class="detail-close" type="button" aria-label="Fermer la fiche du programme"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="m6 6 12 12M18 6 6 18"/></svg></button></div>
      <div class="program-facts">${facts.map((fact) => `<div>${fact}</div>`).join('')}</div>${dynamic}</section>
    ${listSection('Écrit', tables(impact.writes), { open: true })}
    ${listSection('Écrit indirectement (via appels)', tables(impact.indirectWrites), { open: false })}
    ${listSection('Probablement écrit (SQL dynamique)', tables(impact.probableWrites), { open: true })}
    ${listSection('Lit', tables(impact.reads))}
    ${listSection('Probablement lu (SQL dynamique)', tables(impact.probableReads))}
    ${listSection('Appelle', programs(impact.calls))}
    ${listSection('Appelé par', programs(impact.calledBy))}
    ${empty ? '<section class="detail-section"><p class="program-note">Aucune table ni aucun programme détecté : calcul pur, ou SQL non analysable.</p></section>' : ''}
    <details class="detail-section program-section"><summary class="detail-section-heading"><span>Code SQL</span><span>${program.body.length.toLocaleString('fr-FR')} car.</span></summary><button class="button program-copy" type="button">Copier le code</button><pre class="program-body">${esc(program.body)}</pre></details>`;
  detail.querySelector('.detail-close').addEventListener('click', () => { selectedProgramId = null; renderDetail(); });
  detail.querySelector('.program-copy').addEventListener('click', async (event) => {
    try { await navigator.clipboard.writeText(program.body); event.target.textContent = 'Copié'; } catch { event.target.textContent = 'Copie impossible'; }
  });
  bindDetailLinks(detail);
}

function bindDetailLinks(container) {
  container.querySelectorAll('[data-copy]').forEach((button) => button.addEventListener('click', async () => {
    try { await navigator.clipboard.writeText(button.dataset.copy); button.textContent = 'copié'; } catch { button.textContent = 'copie impossible'; }
  }));
  container.querySelectorAll('.reference-button[data-key]').forEach((button) => button.addEventListener('click', () => {
    selectedProgramId = null;
    openTablePage(findByKey(decodeURIComponent(button.dataset.key)), route.view === 'table' ? route.tab : 'colonnes');
  }));
  container.querySelectorAll('.reference-button[data-program-id]').forEach((button) => button.addEventListener('click', () => openProgram(button.dataset.programId)));
}

// Inspector: program sheet first, then the selected data row; collapsed otherwise.
function renderDetail() {
  const inspector = $('#inspector');
  const program = selectedProgramId ? programsById.get(selectedProgramId) : null;
  const row = dataView?.selectedRow != null && route.view === 'table' && route.tab === 'donnees' ? dataView.rows[dataView.selectedRow] : null;
  setInspectorCollapsed(!program && !row);
  if (program) renderProgramDetail(program);
  else if (row) renderRowDetail();
  else { $('#detail').innerHTML = ''; inspector.classList.remove('open'); return; }
  inspector.classList.add('open');
}
