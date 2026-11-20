// Atlas Schema Desk — « Données » tab: grid, filters (kept in the URL), sort, pages, foreign-key navigation.
// Classic script: shares the global scope with the other public/js files, loaded in order by index.html.

let dataView = null; // { key, engine, schema, table, filters, sort, page, pageSize, rows, hasMore, sql, durationMs, loading, error, selectedRow }
let dataTrail = []; // foreign-key hops that led here: { engine, schema, table, filters }
let pendingTrailStep = null; // set just before following a foreign key
let showSensitive = false;
let dataRequestSeq = 0;
let filterDebounce = null;

const SENSITIVE_COLUMN = /pass(word|wd)?\b|passwd|pwd|secret|token|salt|hash|iban|\bbic\b|card_?number|cvv|cvc|api_?key|private_?key/i;
const DATA_PAGE_SIZE = 100;
const RESERVED_PARAMS = new Set(['col']); // URL params that are not data filters

function dataTable() {
  return dataView ? findTable(dataView.engine, dataView.schema, dataView.table) : null;
}

function dataFkByColumn(table) {
  const map = new Map();
  for (const fk of table.foreignKeys || []) if (!map.has(fk.column)) map.set(fk.column, fk);
  return map;
}

function filtersFromParams(table, params) {
  const names = new Set(table.columns.map((column) => column.name));
  return Object.fromEntries(Object.entries(params || {}).filter(([name, value]) => names.has(name) && !RESERVED_PARAMS.has(name) && value !== ''));
}

function stepLabel(step) {
  const filters = Object.entries(step.filters || {}).map(([column, value]) => `${column} ${/^[<>=!]/.test(value) ? value : `~ ${value}`}`);
  return `${step.table}${filters.length ? ` (${filters.join(', ')})` : ''}`;
}

// Follow a foreign key (or a « référencée par » link): records the current step, then navigates.
function followToTable(target, filters) {
  if (!target) return;
  if (dataView) pendingTrailStep = { engine: dataView.engine, schema: dataView.schema, table: dataView.table, filters: { ...dataView.filters } };
  openTablePage(target, 'donnees', filters);
}

function renderDataTab(body, table, params) {
  const filters = filtersFromParams(table, params);
  const key = JSON.stringify([keyOf(table), filters]);
  if (!dataView || dataView.key !== key) {
    if (pendingTrailStep) dataTrail.push(pendingTrailStep);
    else if (!dataView || dataView.table !== table.name || dataView.engine !== table.engine) dataTrail = []; // arrived by another way
    pendingTrailStep = null;
    const keep = dataView && dataView.table === table.name && dataView.engine === table.engine ? { sort: dataView.sort } : { sort: null };
    dataView = { key, engine: table.engine, schema: table.schema, table: table.name, filters, sort: keep.sort, page: 0, pageSize: DATA_PAGE_SIZE, rows: [], hasMore: false, loading: true, error: null, selectedRow: null };
    renderDataGrid(body, table);
    loadDataRows();
    return;
  }
  renderDataGrid(body, table);
}

async function postJson(url, payload) {
  const response = await fetch(url, { method: 'POST', headers: { 'X-DB-Atlas': '1', 'Content-Type': 'application/json' }, body: JSON.stringify(payload) });
  const result = await response.json().catch(() => ({}));
  if (response.status === 404 || response.status === 501) throw new Error('le serveur ne connaît pas cette action : redémarre Atlas Schema Desk (atlas-schema-desk)');
  if (!response.ok) throw new Error(result.error || `HTTP ${response.status}`);
  return result;
}

async function loadDataRows() {
  if (!dataView) return;
  const seq = ++dataRequestSeq;
  const view = dataView;
  view.loading = true;
  view.error = null;
  renderDataStatus();
  try {
    const result = await postJson('api/data/rows', { engine: view.engine, schema: view.schema, table: view.table, filters: view.filters, sort: view.sort, page: view.page, pageSize: view.pageSize });
    if (seq !== dataRequestSeq || view !== dataView) return; // superseded
    Object.assign(view, { rows: result.rows, hasMore: result.hasMore, sql: result.sql, durationMs: result.durationMs, loading: false, selectedRow: null });
  } catch (error) {
    if (seq !== dataRequestSeq || view !== dataView) return;
    Object.assign(view, { rows: [], hasMore: false, loading: false, error: error.message });
  }
  const body = $('#tab-body');
  const table = dataTable();
  if (body && table && route.view === 'table' && route.tab === 'donnees') renderDataGrid(body, table);
  renderDetail();
}

function displayCell(column, value, { full = false } = {}) {
  if (value === null || value === undefined) return '<span class="data-null">NULL</span>';
  if (!showSensitive && SENSITIVE_COLUMN.test(column)) return '<span class="data-masked" title="Colonne sensible masquée">••••••</span>';
  if (typeof value === 'object') {
    if (value.binary) return `<span class="data-null">(binaire, ${value.length.toLocaleString('fr-FR')} octets)</span>`;
    if (value.truncated) return `${esc(full ? value.value : value.value.slice(0, 120))}<span class="data-null"> … (${value.length.toLocaleString('fr-FR')} car.)</span>`;
  }
  const text = String(value);
  return full || text.length <= 120 ? esc(text) : `${esc(text.slice(0, 120))}<span class="data-null">…</span>`;
}

function rawValue(value) {
  return value && typeof value === 'object' ? (value.binary ? null : value.value) : value;
}

function dataDateBadge(engine) {
  const applied = typeof appliedDumps !== 'undefined' ? appliedDumps[engine] : null;
  return applied
    ? `<span class="badge data-date" title="Version S3 ${esc(applied.versionId)} appliquée le ${esc(formatDate(applied.appliedAt))}">données du dump du ${esc(formatDate(applied.lastModified))}</span>`
    : '<span class="badge muted" title="Aucune application de dump enregistrée par Atlas Schema Desk pour cette base">données locales (date du dump inconnue)</span>';
}

function setDataFilters(filters) {
  navigate({ ...route, params: { ...filters } }, { replace: true });
}

function renderDataGrid(body, table) {
  const view = dataView;
  const fks = dataFkByColumn(table);
  const sensitiveCount = table.columns.filter((column) => SENSITIVE_COLUMN.test(column.name)).length;
  const hasFilters = Object.values(view.filters).some(Boolean);
  const trail = dataTrail.length ? `<div class="data-trail">${dataTrail.map((step, index) => `<button class="data-crumb" type="button" data-trail="${index}">${esc(stepLabel(step))}</button><span class="data-crumb-sep">→</span>`).join('')}<span class="data-crumb current">${esc(stepLabel(view))}</span></div>` : '';
  const head = table.columns.map((column) => {
    const sorted = view.sort?.column === column.name ? (view.sort.dir === 'desc' ? ' ▼' : ' ▲') : '';
    const keys = `${column.primary ? '<span class="data-key pk">PK</span>' : ''}${fks.has(column.name) ? '<span class="data-key fk">FK</span>' : ''}`;
    return `<th><button class="data-sort" type="button" data-sort="${esc(column.name)}" title="${esc(column.type)} — trier">${keys}${esc(column.name)}${sorted}</button></th>`;
  }).join('');
  const filters = table.columns.map((column) => {
    const numeric = /int|decimal|numeric|float|double|real|serial|bool|bit/i.test(column.type);
    return `<th><input class="data-filter" type="search" data-filter="${esc(column.name)}" value="${esc(view.filters[column.name] || '')}" placeholder="${numeric ? '= …' : 'contient…'}" aria-label="Filtrer ${esc(column.name)}"></th>`;
  }).join('');
  const editable = canWrite(table.engine) && table.columns.some((column) => column.primary);
  const rows = view.rows.map((row, index) => `<tr data-row="${index}" class="${view.selectedRow === index ? 'selected' : ''}">${table.columns.map((column) => {
    const value = row[column.name];
    if (view.edit && view.edit.rowIndex === index && view.edit.column === column.name) {
      return `<td class="editing"><input class="cell-input" id="cell-input" value="${esc(view.edit.isNull ? '' : view.edit.value)}" placeholder="${view.edit.isNull ? 'NULL' : ''}" aria-label="Nouvelle valeur de ${esc(column.name)}"><button class="cell-null${view.edit.isNull ? ' active' : ''}" type="button" id="cell-null" title="Mettre NULL">NULL</button></td>`;
    }
    const fk = fks.get(column.name);
    const content = displayCell(column.name, value);
    const link = fk && value !== null && rawValue(value) !== null
      ? `<button class="data-fk" type="button" data-fk="${esc(column.name)}" data-row-index="${index}" title="Ouvrir ${esc(fk.targetSchema)}.${esc(fk.targetTable)} où ${esc(fk.targetColumn)} = ${esc(String(rawValue(value)))}">${content}</button>`
      : content;
    return `<td class="${column.primary ? 'pk' : ''}" data-cell-column="${esc(column.name)}"${editable ? ' title="Double-clic : modifier"' : ''}>${link}</td>`;
  }).join('')}</tr>`).join('');
  const empty = !view.loading && !view.error && !view.rows.length ? `<div class="data-empty">Aucune ligne${hasFilters ? ' pour ces filtres' : ''}.</div>` : '';
  const txBanner = sqlTransactions[view.engine]
    ? `<div class="sql-banner"><span>⚠ <strong>Transaction ouverte</strong> sur ${esc(engineLabel(view.engine))} : la grille montre les modifications non validées.</span><button class="button sql-commit" type="button" id="data-commit">Valider</button><button class="button" type="button" id="data-rollback">Annuler</button></div>`
    : '';
  body.innerHTML = `<div class="data-view">
    ${txBanner}${editPreviewMarkup(table)}
    ${trail}
    <div class="data-actions">
      <button class="button" type="button" id="data-clear"${hasFilters ? '' : ' disabled'}>Effacer les filtres</button>
      ${sensitiveCount ? `<button class="button" type="button" id="data-sensitive">${showSensitive ? 'Masquer' : 'Afficher'} les colonnes sensibles (${sensitiveCount})</button>` : ''}
      <span class="data-help">Filtres : texte = contient · <code>=45</code> <code>&gt;=10</code> <code>!=0</code> <code>null</code> <code>!null</code> · valeur bleue = ligne liée · clic sur une ligne = fiche</span>
      <span class="data-pager"><button class="button" type="button" id="data-prev"${view.page ? '' : ' disabled'} aria-label="Page précédente">◀</button><span>page ${view.page + 1}</span><button class="button" type="button" id="data-next"${view.hasMore ? '' : ' disabled'} aria-label="Page suivante">▶</button></span>
    </div>
    <div class="data-scroll"><table class="data-table"><thead><tr>${head}</tr><tr class="data-filter-row">${filters}</tr></thead><tbody>${rows}</tbody></table>${empty}</div>
    <div class="data-status" id="data-status"></div>
  </div>`;
  bindDataGrid(body, table, fks);
  renderDataStatus();
}

function renderDataStatus() {
  const status = $('#data-status');
  if (!status || !dataView) return;
  const view = dataView;
  if (view.loading) status.innerHTML = '<span class="data-loading">Lecture de la base locale…</span>';
  else if (view.error) status.innerHTML = `<span class="data-error">⚠ ${esc(view.error)}</span>`;
  else status.innerHTML = `${view.rows.length} ligne${view.rows.length > 1 ? 's' : ''} · ${view.durationMs} ms · <code class="data-sql" title="Requête générée (valeurs des filtres passées en paramètres)">${esc(view.sql || '')}</code>`;
}

function bindDataGrid(body, table, fks) {
  body.querySelector('#data-sensitive')?.addEventListener('click', () => { showSensitive = !showSensitive; renderDataGrid(body, table); });
  body.querySelector('#data-clear').addEventListener('click', () => setDataFilters({}));
  body.querySelector('#data-prev').addEventListener('click', () => { dataView.page = Math.max(0, dataView.page - 1); loadDataRows(); });
  body.querySelector('#data-next').addEventListener('click', () => { dataView.page += 1; loadDataRows(); });
  body.querySelectorAll('[data-trail]').forEach((button) => button.addEventListener('click', () => {
    const index = Number(button.dataset.trail);
    const step = dataTrail[index];
    dataTrail = dataTrail.slice(0, index);
    dataView = null; // force a fresh view for that step
    openTablePage(findTable(step.engine, step.schema, step.table), 'donnees', step.filters);
  }));
  body.querySelectorAll('[data-sort]').forEach((button) => button.addEventListener('click', () => {
    const column = button.dataset.sort;
    const current = dataView.sort?.column === column ? dataView.sort.dir : null;
    dataView.sort = current === 'asc' ? { column, dir: 'desc' } : current === 'desc' ? null : { column, dir: 'asc' };
    dataView.page = 0;
    loadDataRows();
  }));
  body.querySelectorAll('[data-filter]').forEach((input) => input.addEventListener('input', () => {
    clearTimeout(filterDebounce);
    filterDebounce = setTimeout(() => {
      const focused = input.dataset.filter;
      const caret = input.selectionStart;
      const next = { ...dataView.filters, [focused]: input.value };
      if (!input.value) delete next[focused];
      setDataFilters(next);
      const again = document.querySelector(`[data-filter="${CSS.escape(focused)}"]`);
      if (again) { again.focus(); again.setSelectionRange(caret, caret); }
    }, 450);
  }));
  body.querySelectorAll('[data-fk]').forEach((button) => button.addEventListener('click', (event) => {
    event.stopPropagation();
    const fk = fks.get(button.dataset.fk);
    const value = rawValue(dataView.rows[Number(button.dataset.rowIndex)][button.dataset.fk]);
    followToTable(findTable(table.engine, fk.targetSchema, fk.targetTable), { [fk.targetColumn]: `=${value}` });
  }));
  bindCellEditing(body, table);
  body.querySelector('#data-commit')?.addEventListener('click', () => endDataTransaction('commit'));
  body.querySelector('#data-rollback')?.addEventListener('click', () => endDataTransaction('rollback'));
  body.querySelectorAll('tr[data-row]').forEach((row) => row.addEventListener('click', () => {
    if (dataView.edit) return; // clicks inside an edited row stay in the editor
    dataView.selectedRow = Number(row.dataset.row);
    body.querySelectorAll('tr[data-row].selected').forEach((other) => other.classList.remove('selected'));
    row.classList.add('selected');
    selectedProgramId = null;
    renderDetail();
  }));
}

// ---- Cell editing: UPDATE … WHERE <primary key> = …, previewed, run in the base's transaction
function quoteIdent(engine, name) {
  return driverOf(engine) === 'mysql' ? `\`${name.replace(/`/g, '``')}\`` : `"${name.replace(/"/g, '""')}"`;
}

function cellUpdate(table) {
  const edit = dataView.edit;
  const row = dataView.rows[edit.rowIndex];
  const pk = table.columns.filter((column) => column.primary);
  const q = (name) => quoteIdent(table.engine, name);
  const sql = `UPDATE ${q(table.schema)}.${q(table.name)} SET ${q(edit.column)} = ${edit.isNull ? 'NULL' : '?'} WHERE ${pk.map((column) => `${q(column.name)} = ?`).join(' AND ')}`;
  const params = [...(edit.isNull ? [] : [edit.value]), ...pk.map((column) => rawValue(row[column.name]))];
  return { sql, params, pk };
}

function editPreviewMarkup(table) {
  const edit = dataView?.edit;
  if (!edit?.preview) return '';
  const { sql, params } = cellUpdate(table);
  const shown = params.map((value) => (value === null ? 'NULL' : `'${String(value).slice(0, 80)}'`)).join(', ');
  return `<div class="sql-confirm edit-preview"><strong>Modifier la cellule ${esc(edit.column)} ?</strong>
    <code>${esc(sql)}</code><span class="muted small">paramètres : ${esc(shown)}</span>
    ${triggerMarkup(table.engine, sql)}
    ${edit.error ? `<div class="data-error">⚠ ${esc(edit.error)}</div>` : ''}
    <div><button class="button sql-run" type="button" id="edit-run">Exécuter (dans la transaction)</button> <button class="button" type="button" id="edit-cancel">Annuler la modification</button></div></div>`;
}

function bindCellEditing(body, table) {
  if (!canWrite(table.engine)) return;
  const pkColumns = table.columns.filter((column) => column.primary);
  body.querySelectorAll('td[data-cell-column]').forEach((cell) => cell.addEventListener('dblclick', (event) => {
    event.preventDefault();
    const rowIndex = Number(cell.closest('tr').dataset.row);
    const column = cell.dataset.cellColumn;
    const value = dataView.rows[rowIndex][column];
    if (!pkColumns.length) { showLoadBanner(`${table.name} n'a pas de clé primaire : modifie-la avec la console SQL.`); return; }
    if (value && typeof value === 'object') { showLoadBanner('Valeur binaire ou trop longue : modifie-la avec la console SQL.'); return; }
    if (pkColumns.some((pk) => rawValue(dataView.rows[rowIndex][pk.name]) === null)) return;
    dataView.edit = { rowIndex, column, value: value ?? '', isNull: value === null, original: value, preview: false };
    renderDataGrid(body, table);
    const input = body.querySelector('#cell-input');
    input.focus();
    input.select();
  }));
  const input = body.querySelector('#cell-input');
  if (input) {
    input.addEventListener('input', () => { dataView.edit.value = input.value; dataView.edit.isNull = false; body.querySelector('#cell-null')?.classList.remove('active'); });
    input.addEventListener('keydown', (event) => {
      if (event.key === 'Enter') { event.preventDefault(); previewCellEdit(body, table); }
      if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); dataView.edit = null; renderDataGrid(body, table); }
    });
    body.querySelector('#cell-null').addEventListener('click', (event) => { event.stopPropagation(); dataView.edit.isNull = true; dataView.edit.value = ''; previewCellEdit(body, table); });
  }
  body.querySelector('#edit-run')?.addEventListener('click', () => runCellEdit(body, table));
  body.querySelector('#edit-cancel')?.addEventListener('click', () => { dataView.edit = null; renderDataGrid(body, table); });
}

function previewCellEdit(body, table) {
  const edit = dataView.edit;
  const unchanged = edit.isNull ? edit.original === null : edit.original !== null && String(edit.original) === edit.value;
  if (unchanged) { dataView.edit = null; renderDataGrid(body, table); return; }
  edit.preview = true;
  renderDataGrid(body, table);
}

async function runCellEdit(body, table) {
  const { sql, params } = cellUpdate(table);
  try {
    const result = await postJson('api/sql', { engine: table.engine, sql, params });
    if (result.error) throw new Error(result.error.message);
    const affected = result.results[0]?.affected;
    sqlTransactions[table.engine] = result.txOpen;
    renderTransactionIndicator();
    dataView.edit = null;
    if (affected !== 1) showLoadBanner(`Attention : ${affected} ligne(s) modifiée(s) au lieu d'une. Annule la transaction si ce n'est pas voulu.`);
    loadDataRows(); // same session: the grid shows the pending change
  } catch (error) {
    dataView.edit.error = error.message;
    renderDataGrid(body, table);
  }
}

async function endDataTransaction(action) {
  const engine = dataView.engine;
  try {
    await postJson(`api/sql/${action}`, { engine });
    sqlTransactions[engine] = false;
    renderTransactionIndicator();
    loadDataRows();
  } catch (error) {
    showLoadBanner(`${action === 'commit' ? 'Validation' : 'Annulation'} impossible : ${error.message}`);
  }
}

// ---- Row sheet in the inspector, with the rows that reference it
const refsCache = new Map();

function renderRowDetail() {
  const detail = $('#detail');
  const table = dataTable();
  const row = dataView.rows[dataView.selectedRow];
  const fks = dataFkByColumn(table);
  const pk = table.columns.filter((column) => column.primary).map((column) => `${column.name} = ${rawValue(row[column.name])}`).join(', ');
  const fields = table.columns.map((column) => {
    const value = row[column.name];
    const fk = fks.get(column.name);
    const content = displayCell(column.name, value, { full: true });
    const link = fk && rawValue(value) !== null && value !== null ? `<button class="data-fk" type="button" data-sheet-fk="${esc(column.name)}">${content}</button>` : content;
    return `<div class="row-field"><span class="row-field-name${column.primary ? ' pk' : ''}" title="${esc(column.type)}">${esc(column.name)}</span><span class="row-field-value">${link}</span></div>`;
  }).join('');
  detail.innerHTML = `<section class="detail-main"><div class="detail-top"><div><div class="detail-engine">${esc(engineLabel(table.engine))} · ${esc(table.schema)}.${esc(table.name)}</div><h3 class="detail-title">Ligne ${esc(pk || `n° ${dataView.page * dataView.pageSize + dataView.selectedRow + 1}`)}</h3></div><button class="detail-close" type="button" aria-label="Fermer la fiche de la ligne"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="m6 6 12 12M18 6 6 18"/></svg></button></div></section>
    <section class="detail-section"><div class="detail-section-heading"><span>Référencée par</span><span id="row-refs-count">…</span></div><div class="reference-list" id="row-refs"><p class="program-note">Recherche des lignes liées…</p></div></section>
    <section class="detail-section"><div class="detail-section-heading"><span>Colonnes</span><span>${table.columns.length}</span></div><div class="row-fields">${fields}</div></section>`;
  detail.querySelector('.detail-close').addEventListener('click', closeRowDetail);
  detail.querySelectorAll('[data-sheet-fk]').forEach((button) => button.addEventListener('click', () => {
    const fk = fks.get(button.dataset.sheetFk);
    followToTable(findTable(table.engine, fk.targetSchema, fk.targetTable), { [fk.targetColumn]: `=${rawValue(row[button.dataset.sheetFk])}` });
  }));
  loadRowRefs(table, row);
}

function closeRowDetail() {
  if (!dataView) return;
  dataView.selectedRow = null;
  document.querySelectorAll('tr[data-row].selected').forEach((row) => row.classList.remove('selected'));
  renderDetail();
}

async function loadRowRefs(table, row) {
  const plainRow = Object.fromEntries(Object.entries(row).map(([name, value]) => [name, rawValue(value)]));
  const cacheKey = JSON.stringify([table.engine, table.schema, table.name, plainRow]);
  const container = () => document.querySelector('#row-refs');
  let refs = refsCache.get(cacheKey);
  if (!refs) {
    try {
      refs = (await postJson('api/data/refs', { engine: table.engine, schema: table.schema, table: table.name, row: plainRow })).refs;
      refsCache.set(cacheKey, refs);
    } catch (error) {
      if (container()) container().innerHTML = `<p class="data-error">⚠ ${esc(error.message)}</p>`;
      return;
    }
  }
  if (!container()) return; // the sheet was closed meanwhile
  const found = refs.filter((ref) => ref.count > 0);
  const zero = refs.filter((ref) => ref.count === 0).length;
  const failed = refs.filter((ref) => ref.error).length;
  document.querySelector('#row-refs-count').textContent = String(found.length);
  container().innerHTML = found.map((ref, index) => `<button class="reference-button program-ref" type="button" data-ref="${index}"><span class="reference-copy">${esc(ref.schema)}.${esc(ref.table)}.${esc(ref.column)} <span class="program-extra">${ref.capped ? `${ref.count}+` : ref.count} ligne${ref.count > 1 ? 's' : ''}</span></span><span class="reference-arrow">↗</span></button>`).join('')
    + (found.length ? '' : '<p class="program-note">Aucune ligne d’une autre table ne pointe vers celle-ci (clés étrangères déclarées).</p>')
    + (zero ? `<p class="program-note">${zero} autre${zero > 1 ? 's' : ''} relation${zero > 1 ? 's' : ''} déclarée${zero > 1 ? 's' : ''} sans ligne.</p>` : '')
    + (failed ? `<p class="data-error">${failed} comptage${failed > 1 ? 's' : ''} en erreur.</p>` : '');
  container().querySelectorAll('[data-ref]').forEach((button) => button.addEventListener('click', () => {
    const ref = found[Number(button.dataset.ref)];
    followToTable(findTable(table.engine, ref.schema, ref.table), { [ref.column]: `=${ref.value}` });
  }));
}
