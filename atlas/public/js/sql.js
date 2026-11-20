// Atlas Schema Desk — SQL console: CodeMirror editor (highlighting, schema-aware autocomplete, formatting),
// free SQL on the local databases, writes kept in a transaction until « Valider » / « Annuler »,
// live announcement of the triggers a write will fire.
// Classic script: shares the global scope with the other public/js files, loaded in order by index.html.

const SQL_DEFAULT_SCHEMA = {};
const SQL_HISTORY_KEY = 'db-atlas-sql-history';
const SQL_HISTORY_MAX = 50;
const SQL_KIND_LABEL = { read: 'lecture', write: 'écriture', ddl: 'structure', session: 'session' };
const sqlState = {}; // engine -> { text, prefilledText, running, response, error, confirmations, ranText, notice }
let sqlTransactions = {}; // engine -> open transaction (from the server)
let sqlTransactionsLostAt = null;
let sqlEditor = null; // current CodeMirror instance (null: plain textarea fallback)
const sqlHintCache = {}; // engine -> { size, tables }

function sqlStateFor(engine) {
  if (!sqlState[engine]) sqlState[engine] = { text: '', prefilledText: null, running: false, response: null, error: null, confirmations: null, ranText: '', notice: null };
  return sqlState[engine];
}

async function refreshTransactionState() {
  try {
    const status = await fetchExportStatus();
    sqlTransactions = status.transactions || {};
    sqlTransactionsLostAt = status.transactionsLostAt || null;
  } catch { /* older server: no console */ }
  renderTransactionIndicator();
}

// Top-bar reminder: an open transaction holds locks and is invisible to other sessions.
function renderTransactionIndicator() {
  const open = ENGINES.filter((engine) => sqlTransactions[engine]);
  const indicator = $('#tx-indicator');
  indicator.hidden = !open.length;
  indicator.textContent = open.length ? `⚠ Transaction ${open.map(engineLabel).join(', ')}` : '';
  indicator.dataset.engine = open[0] || '';
}

function loadSqlHistory() {
  try { return JSON.parse(localStorage.getItem(SQL_HISTORY_KEY) || '[]'); } catch { return []; }
}

function rememberSql(engine, text) {
  const list = [{ engine, sql: text, at: new Date().toISOString() }, ...loadSqlHistory().filter((item) => !(item.engine === engine && item.sql === text))].slice(0, SQL_HISTORY_MAX);
  try { localStorage.setItem(SQL_HISTORY_KEY, JSON.stringify(list)); } catch { /* storage unavailable */ }
}

// ---- trigger announcement (client side, from the programs catalog)
const SQL_WRITE_TARGET = /^\s*(?:(UPDATE)\s+(?:LOW_PRIORITY\s+|IGNORE\s+|ONLY\s+)*|(INSERT)\s+(?:IGNORE\s+)?INTO\s+|(REPLACE)\s+(?:INTO\s+)?|(DELETE)\s+(?:LOW_PRIORITY\s+|QUICK\s+|IGNORE\s+)*FROM\s+)([`"\w$]+(?:\.[`"\w$]+)?)/i;

function resolveSqlTable(engine, name) {
  const parts = name.replace(/[`"]/g, '').split('.');
  if (parts.length === 2) return all.find((t) => t.engine === engine && t.schema.toLowerCase() === parts[0].toLowerCase() && t.name.toLowerCase() === parts[1].toLowerCase());
  const lower = parts[0].toLowerCase();
  const inDefault = all.find((t) => t.engine === engine && t.schema === SQL_DEFAULT_SCHEMA[engine] && t.name.toLowerCase() === lower);
  if (inDefault) return inDefault;
  const matches = all.filter((t) => t.engine === engine && t.name.toLowerCase() === lower);
  return matches.length === 1 ? matches[0] : null;
}

function triggerAnnouncements(engine, text) {
  const notes = [];
  const withoutComments = text.replace(/\/\*[\s\S]*?\*\//g, ' ').replace(/--[^\n]*/g, ' ');
  for (const statement of withoutComments.split(';')) {
    const match = SQL_WRITE_TARGET.exec(statement);
    if (!match) continue;
    const event = (match[1] || match[2] || match[3] || match[4]).toUpperCase().replace('REPLACE', 'INSERT');
    const table = resolveSqlTable(engine, match[5]);
    if (!table) continue;
    const triggers = (tableImpact(table)?.triggers || []).map((id) => programsById.get(id)).filter((program) => program && (program.events || []).includes(event));
    notes.push({ event, table, triggers });
  }
  return notes;
}

function triggerMarkup(engine, text) {
  const notes = triggerAnnouncements(engine, text);
  if (!notes.length) return '';
  return `<div class="sql-triggers">${notes.map(({ event, table, triggers }) => (triggers.length
    ? `<div>⚡ ${esc(event)} sur <a href="${esc(tableHref(table, 'programmes'))}">${esc(table.schema)}.${esc(table.name)}</a> déclenchera : ${triggers.map((program) => {
      const writes = new Set([...(program.impact?.writes || []), ...(program.impact?.indirectWrites || [])]).size;
      return `<button class="link-button" type="button" data-program-id="${esc(program.id)}">${esc(program.name)}</button> <span class="muted">(${esc(program.timing || '')}${writes ? `, écrit ${writes} table${writes > 1 ? 's' : ''}` : ''})</span>`;
    }).join(', ')}</div>`
    : `<div class="muted">${esc(event)} sur ${esc(table.schema)}.${esc(table.name)} : aucun trigger.</div>`)).join('')}</div>`;
}

// ---- autocomplete source: { "table": [cols], "schema.table": [cols] } for CodeMirror's sql-hint
function sqlHintTables(engine) {
  const tables = all.filter((table) => table.engine === engine);
  const cached = sqlHintCache[engine];
  if (cached && cached.size === tables.length && cached.all === all) return cached.tables;
  const result = {};
  for (const table of tables) {
    const columns = table.columns.map((column) => column.name);
    result[`${table.schema}.${table.name}`] = columns;
    if (table.schema === SQL_DEFAULT_SCHEMA[engine]) result[table.name] = columns;
  }
  sqlHintCache[engine] = { size: tables.length, all, tables: result };
  return result;
}

// Stored functions/procedures of a base, for completion: short names in the default schema, qualified otherwise.
function routineHints(engine) {
  const routines = [];
  for (const program of programsById.values()) {
    if (programEngine(program.id) !== engine || !['function', 'procedure'].includes(program.kind)) continue;
    routines.push({ program, names: program.schema === SQL_DEFAULT_SCHEMA[engine] ? [program.name, `${program.schema}.${program.name}`] : [`${program.schema}.${program.name}`] });
  }
  return routines;
}

// CodeMirror's SQL hint (keywords, tables, columns) + stored routines from the programs catalog.
// After CALL only procedures are proposed; choosing one inserts « name() » with the cursor inside.
function atlasSqlHint(cm, options, engine) {
  const base = CodeMirror.hint.sql(cm, options);
  const cursor = cm.getCursor();
  const before = cm.getLine(cursor.line).slice(0, cursor.ch);
  const word = (/[\w$.]*$/.exec(before) || [''])[0];
  const afterCall = /\bCALL\s+[\w$.]*$/i.test(before);
  const token = cm.getTokenAt(cursor);
  if (!word || /string|comment/.test(token.type || '')) return base;
  const from = CodeMirror.Pos(cursor.line, cursor.ch - word.length);
  const lowered = word.toLowerCase();
  const kinds = afterCall ? ['procedure'] : ['function', 'procedure'];
  const seen = new Set();
  const routines = [];
  for (const { program, names } of routineHints(engine)) {
    if (!kinds.includes(program.kind)) continue;
    const name = names.find((candidate) => candidate.toLowerCase().startsWith(lowered));
    if (!name || seen.has(name)) continue;
    seen.add(name);
    routines.push({
      text: name,
      displayText: `${name}  · ${program.kind === 'procedure' ? 'procédure' : 'fonction'}`,
      className: `CodeMirror-hint-routine ${program.kind}`,
      hint: (editor, data, completion) => {
        editor.replaceRange(`${completion.text}()`, completion.from || data.from, completion.to || data.to, 'complete');
        const end = editor.getCursor();
        editor.setCursor(CodeMirror.Pos(end.line, end.ch - 1));
      },
    });
    if (routines.length >= 40) break;
  }
  routines.sort((a, b) => a.text.length - b.text.length || a.text.localeCompare(b.text));
  if (!routines.length) return base;
  const others = (base?.list || []).filter((item) => !seen.has(typeof item === 'string' ? item : item.text));
  return { list: afterCall ? routines : [...routines, ...others], from: base?.from || from, to: base?.to || cursor };
}

// ---- rendering: the editor is created once per render of the page; actions/output update in place
function renderSqlPage(main, engine) {
  main.innerHTML = `<div class="page"><header class="page-header"><h1 class="page-title">Console SQL</h1>
    <div class="sql-engines">${ENGINES.map((name) => `<a class="tab${name === engine ? ' active' : ''}" href="#/sql/${name}">${esc(engineLabel(name))}${sqlTransactions[name] ? ' ⚠' : ''}</a>`).join('')}</div></header>
    <div class="tab-body" id="sql-body"></div></div>`;
  renderSqlConsole(main.querySelector('#sql-body'), engine, null);
}

function renderSqlConsole(body, engine, table) {
  const state = sqlStateFor(engine);
  const prefill = table ? `SELECT *\nFROM ${table.schema}.${table.name}\nLIMIT 100;` : '';
  // Prefill for this table unless the user typed something of their own.
  if (table && (!state.text.trim() || state.text === state.prefilledText)) {
    state.text = prefill;
    state.prefilledText = prefill;
  }
  body.innerHTML = `<div class="sql-console">
    <div class="sql-hint">${esc(engineLabel(engine))} · ${canWrite(engine) ? 'écriture activée' : 'lecture seule'} · schéma par défaut <code>${esc(SQL_DEFAULT_SCHEMA[engine])}</code> · <kbd>Ctrl</kbd>+<kbd>Entrée</kbd> exécuter (la sélection seule si du texte est sélectionné) · <kbd>Ctrl</kbd>+<kbd>Espace</kbd> compléter · <kbd>Ctrl</kbd>+<kbd>/</kbd> commenter · <kbd>Maj</kbd>+<kbd>Alt</kbd>+<kbd>F</kbd> mettre en forme</div>
    <div id="sql-banners"></div>
    <textarea class="sql-editor" id="sql-editor" spellcheck="false" aria-label="Requête SQL" placeholder="SELECT … ;   UPDATE … WHERE … ;">${esc(state.text)}</textarea>
    <div id="sql-triggers">${triggerMarkup(engine, state.text)}</div>
    <div class="sql-actions" id="sql-actions"></div>
    <div class="sql-output" id="sql-output"></div>
  </div>`;
  createSqlEditor(body, engine, table);
  updateSqlConsole(body, engine, table);
  bindDetailLinks(body.querySelector('#sql-triggers'));
}

function editorText() {
  return sqlEditor ? sqlEditor.getValue() : ($('#sql-editor')?.value || '');
}

function createSqlEditor(body, engine, table) {
  const state = sqlStateFor(engine);
  const textarea = body.querySelector('#sql-editor');
  const onChange = (text) => {
    state.text = text;
    clearSqlErrorMark();
    clearTimeout(createSqlEditor.timer);
    createSqlEditor.timer = setTimeout(() => {
      const box = body.querySelector('#sql-triggers');
      if (box) { box.innerHTML = triggerMarkup(engine, state.text); bindDetailLinks(box); }
    }, 250);
    updateRunLabel(body);
  };
  if (typeof CodeMirror === 'undefined') { // vendor files missing: plain textarea still works
    sqlEditor = null;
    textarea.addEventListener('input', () => onChange(textarea.value));
    textarea.addEventListener('keydown', (event) => {
      if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') { event.preventDefault(); runSql(body, engine, table); }
    });
    return;
  }
  const run = () => runSql(body, engine, table);
  sqlEditor = CodeMirror.fromTextArea(textarea, {
    mode: driverOf(engine) === 'mysql' ? 'text/x-mysql' : 'text/x-pgsql',
    theme: 'atlas',
    lineNumbers: true,
    matchBrackets: true,
    autoCloseBrackets: true,
    styleActiveLine: true,
    indentUnit: 2,
    tabSize: 2,
    lineWrapping: true,
    extraKeys: {
      'Ctrl-Enter': run, 'Cmd-Enter': run,
      'Ctrl-Space': 'autocomplete',
      'Ctrl-/': 'toggleComment', 'Cmd-/': 'toggleComment',
      'Shift-Alt-F': () => formatSql(engine),
      Tab: (cm) => (cm.somethingSelected() ? cm.indentSelection('add') : cm.replaceSelection('  ')),
    },
    hintOptions: { tables: sqlHintTables(engine), completeSingle: false, closeOnUnfocus: true, hint: (cm, options) => atlasSqlHint(cm, options, engine) },
  });
  sqlEditor.on('change', (cm) => onChange(cm.getValue()));
  sqlEditor.on('cursorActivity', () => updateRunLabel(body));
  // Suggest while typing (letters, digits, _ and the dot of table.column), not inside strings/comments.
  sqlEditor.on('inputRead', (cm, change) => {
    if (cm.state.completionActive || change.origin !== '+input') return;
    const typed = change.text.join('');
    if (!/^[\w.]$/.test(typed)) return;
    const token = cm.getTokenAt(cm.getCursor());
    if (/string|comment|number/.test(token.type || '')) return;
    if (typed !== '.' && token.string.replace(/[^\w]/g, '').length < 2) return;
    cm.showHint({ completeSingle: false });
  });
  setTimeout(() => sqlEditor && sqlEditor.refresh(), 0);
}

function updateRunLabel(body) {
  const button = body.querySelector('#sql-run');
  if (!button || sqlStateFor(currentSqlEngine()).running) return;
  button.textContent = sqlEditor && sqlEditor.somethingSelected() ? 'Exécuter la sélection' : 'Exécuter';
}

function currentSqlEngine() {
  return route.view === 'sql' ? route.engine : (currentTable()?.engine || ENGINES[0]);
}

function formatSql(engine) {
  if (typeof sqlFormatter === 'undefined') { showLoadBanner('Mise en forme indisponible (scripts/vendor-editor.sh non lancé ?)'); return; }
  const options = { language: driverOf(engine) === 'mysql' ? 'mysql' : 'postgresql', keywordCase: 'upper', tabWidth: 2, linesBetweenQueries: 1 };
  try {
    if (sqlEditor) {
      if (sqlEditor.somethingSelected()) sqlEditor.replaceSelection(sqlFormatter.format(sqlEditor.getSelection(), options), 'around');
      else sqlEditor.setValue(sqlFormatter.format(sqlEditor.getValue(), options));
    } else {
      const textarea = $('#sql-editor');
      textarea.value = sqlFormatter.format(textarea.value, options);
      textarea.dispatchEvent(new Event('input'));
    }
  } catch (error) {
    showLoadBanner(`Mise en forme impossible : ${error.message}`);
  }
}

// Banners, buttons and output are re-rendered; the editor (cursor, undo history) is not.
function updateSqlConsole(body, engine, table) {
  const state = sqlStateFor(engine);
  const open = sqlTransactions[engine];
  body.querySelector('#sql-banners').innerHTML = `${sqlTransactionsLostAt ? `<div class="sql-banner danger">Le worker a redémarré le ${esc(formatDate(sqlTransactionsLostAt))} : les transactions qui étaient ouvertes ont été annulées par la base.</div>` : ''}
    ${open ? `<div class="sql-banner"><span>⚠ <strong>Transaction ouverte</strong> sur ${esc(engineLabel(engine))} : modifications non validées (invisibles ailleurs, verrous posés).</span><button class="button sql-commit" type="button" id="sql-commit">Valider</button><button class="button" type="button" id="sql-rollback">Annuler</button></div>` : ''}`;
  const history = loadSqlHistory().filter((item) => item.engine === engine);
  body.querySelector('#sql-actions').innerHTML = `
    <button class="button sql-run" type="button" id="sql-run"${state.running ? ' disabled' : ''}>${state.running ? 'Exécution…' : 'Exécuter'}</button>
    <button class="button" type="button" id="sql-format" title="Maj+Alt+F">Mettre en forme</button>
    <select id="sql-history" aria-label="Historique des requêtes"><option value="">Historique (${history.length})…</option>${history.map((item, index) => `<option value="${index}">${esc(item.sql.replace(/\s+/g, ' ').slice(0, 90))}</option>`).join('')}</select>
    ${state.response?.results?.some((result) => result.columns) ? '<button class="button" type="button" id="sql-csv">Export CSV</button>' : ''}`;
  body.querySelector('#sql-output').innerHTML = state.notice ? `<div class="sql-banner ok">${esc(state.notice)}</div>` : sqlOutputMarkup(engine);
  state.notice = null;
  updateRunLabel(body);
  bindSqlActions(body, engine, table);
  markSqlError(engine);
}

function sqlOutputMarkup(engine) {
  const state = sqlStateFor(engine);
  if (state.confirmations) {
    return `<div class="sql-confirm"><strong>Confirmation demandée</strong>${state.confirmations.map((item) => `<div class="sql-confirm-item"><code>${esc(item.sql)}</code><ul>${item.reasons.map((reason) => `<li>${esc(reason)}</li>`).join('')}</ul></div>`).join('')}
      <div><button class="button danger" type="button" id="sql-confirm">Exécuter quand même</button> <button class="button" type="button" id="sql-cancel">Ne pas exécuter</button></div></div>`;
  }
  if (state.error) return `<div class="data-error sql-error">⚠ ${esc(state.error)}</div>`;
  const response = state.response;
  if (!response) return '<p class="muted small">Résultats ici. Les lectures s\'exécutent directement ; un INSERT/UPDATE/DELETE ouvre une transaction.</p>';
  const blocks = response.results.map((result) => {
    const statement = response.statements[result.index];
    const head = `<div class="sql-result-head"><span class="sql-kind ${result.kind}">${esc(SQL_KIND_LABEL[result.kind] || result.kind)}</span><code>${esc(statement.sql.replace(/\s+/g, ' ').slice(0, 140))}</code><span class="muted">${result.durationMs} ms</span></div>`;
    if (!result.columns) return `<div class="sql-result">${head}<div class="sql-affected">${result.affected} ligne${result.affected > 1 ? 's' : ''} ${result.kind === 'read' ? 'concernée' : 'modifiée'}${result.affected > 1 ? 's' : ''}</div></div>`;
    const view = resultView(state, result.index);
    const header = result.columns.map((column, index) => `<th><button class="data-sort" type="button" data-result-sort="${result.index}:${index}" title="Trier (sur les lignes affichées)">${esc(column)}${view.sort?.column === index ? (view.sort.dir === 'asc' ? ' ▲' : ' ▼') : ''}</button></th>`).join('');
    const filters = result.columns.map((column, index) => `<th><input class="data-filter" type="search" data-result-filter="${result.index}:${index}" value="${esc(view.filters[index] || '')}" placeholder="filtrer…" aria-label="Filtrer ${esc(column)}"></th>`).join('');
    return `<div class="sql-result" data-result="${result.index}">${head}<div class="data-scroll sql-grid"><table class="data-table"><thead><tr>${header}</tr>${result.rows.length > 1 ? `<tr class="data-filter-row">${filters}</tr>` : ''}</thead><tbody>${resultRowsMarkup(result, view)}</tbody></table>${result.rows.length ? '' : '<div class="data-empty">0 ligne</div>'}</div>
      <div class="muted small" data-result-count="${result.index}">${resultCountText(result, view)}</div></div>`;
  }).join('');
  const failed = response.error ? `<div class="data-error sql-error">⚠ Instruction ${response.error.index + 1} en échec (surlignée dans l'éditeur) : ${esc(response.error.message)}${response.txOpen ? '<br>Les instructions précédentes restent dans la transaction ouverte.' : ''}${response.error.index < response.statements.length - 1 ? `<br>${response.statements.length - response.error.index - 1} instruction(s) suivante(s) non exécutée(s).` : ''}</div>` : '';
  const implicit = response.implicitCommit ? '<div class="sql-banner">La transaction en cours a été validée implicitement par l\'instruction de structure (MySQL).</div>' : '';
  return implicit + blocks + failed;
}

// ---- console results: sort and filter in the browser, on the rows already received (≤ 500)
function resultView(state, index) {
  state.views = state.views || {};
  state.views[index] = state.views[index] || { sort: null, filters: {} };
  return state.views[index];
}

// Same syntax as the data grid: text = contains (case-insensitive) · =x · !=x · >n <n >=n <=n · null · !null
function matchesFilter(value, filter) {
  const raw = rawValue(value);
  const text = filter.trim();
  if (!text) return true;
  const lowered = text.toLowerCase();
  if (lowered === 'null') return raw === null || raw === undefined;
  if (lowered === '!null') return raw !== null && raw !== undefined;
  if (raw === null || raw === undefined) return false;
  const match = /^(<=|>=|!=|<>|<|>|=)\s*(.*)$/.exec(text);
  if (!match) return String(raw).toLowerCase().includes(lowered);
  const [, operator, operand] = match;
  const bothNumbers = operand !== '' && !Number.isNaN(Number(operand)) && !Number.isNaN(Number(raw));
  const left = bothNumbers ? Number(raw) : String(raw);
  const right = bothNumbers ? Number(operand) : operand;
  switch (operator) {
    case '=': return left === right;
    case '!=': case '<>': return left !== right;
    case '<': return left < right;
    case '>': return left > right;
    case '<=': return left <= right;
    default: return left >= right;
  }
}

function compareValues(a, b) {
  const left = rawValue(a);
  const right = rawValue(b);
  if (left === null || left === undefined) return right === null || right === undefined ? 0 : 1; // NULL last
  if (right === null || right === undefined) return -1;
  const numeric = !Number.isNaN(Number(left)) && !Number.isNaN(Number(right)) && String(left).trim() !== '' && String(right).trim() !== '';
  return numeric ? Number(left) - Number(right) : String(left).localeCompare(String(right), 'fr', { numeric: true });
}

function visibleRows(result, view) {
  const filters = Object.entries(view.filters).filter(([, text]) => text && text.trim());
  let rows = filters.length ? result.rows.filter((row) => filters.every(([index, text]) => matchesFilter(row[index], text))) : result.rows;
  if (view.sort) {
    const { column, dir } = view.sort;
    const isNull = (value) => rawValue(value) === null || rawValue(value) === undefined;
    rows = [...rows].sort((a, b) => {
      if (isNull(a[column]) || isNull(b[column])) return compareValues(a[column], b[column]); // NULL last in both directions
      return compareValues(a[column], b[column]) * (dir === 'desc' ? -1 : 1);
    });
  }
  return rows;
}

function resultRowsMarkup(result, view) {
  return visibleRows(result, view).map((row) => `<tr>${row.map((value, index) => `<td>${displayCell(result.columns[index], value)}</td>`).join('')}</tr>`).join('');
}

function resultCountText(result, view) {
  const shown = visibleRows(result, view).length;
  const total = result.rows.length;
  const filtered = shown !== total ? `${shown} / ${total} lignes (filtrées)` : `${total} ligne${total > 1 ? 's' : ''}`;
  return `${filtered}${result.hasMore ? ' — limite de 500 atteinte : le tri et les filtres ne portent que sur ces lignes (ajoute un ORDER BY / WHERE pour la base entière)' : ''}`;
}

function bindResultControls(body, engine) {
  const state = sqlStateFor(engine);
  const resultFor = (index) => state.response.results.find((result) => result.index === index);
  body.querySelectorAll('[data-result-sort]').forEach((button) => button.addEventListener('click', () => {
    const [index, column] = button.dataset.resultSort.split(':').map(Number);
    const view = resultView(state, index);
    const current = view.sort?.column === column ? view.sort.dir : null;
    view.sort = current === 'asc' ? { column, dir: 'desc' } : current === 'desc' ? null : { column, dir: 'asc' };
    const block = body.querySelector(`.sql-result[data-result="${index}"]`);
    block.querySelectorAll('[data-result-sort]').forEach((other) => {
      const otherColumn = Number(other.dataset.resultSort.split(':')[1]);
      other.textContent = resultFor(index).columns[otherColumn] + (view.sort?.column === otherColumn ? (view.sort.dir === 'asc' ? ' ▲' : ' ▼') : '');
    });
    refreshResultBlock(body, engine, index);
  }));
  body.querySelectorAll('[data-result-filter]').forEach((input) => input.addEventListener('input', () => {
    const [index, column] = input.dataset.resultFilter.split(':').map(Number);
    resultView(state, index).filters[column] = input.value;
    clearTimeout(bindResultControls.timer);
    bindResultControls.timer = setTimeout(() => refreshResultBlock(body, engine, index), 150);
  }));
}

// Only the rows and the count are redrawn, so the filter input keeps focus and caret.
function refreshResultBlock(body, engine, index) {
  const state = sqlStateFor(engine);
  const result = state.response.results.find((item) => item.index === index);
  const block = body.querySelector(`.sql-result[data-result="${index}"]`);
  if (!result || !block) return;
  const view = resultView(state, index);
  block.querySelector('tbody').innerHTML = resultRowsMarkup(result, view);
  block.querySelector(`[data-result-count="${index}"]`).textContent = resultCountText(result, view);
}

// Highlight the failing statement in the editor (first occurrence of its text).
let sqlErrorMark = null;
function clearSqlErrorMark() {
  if (sqlErrorMark) { sqlErrorMark.clear(); sqlErrorMark = null; }
}

function markSqlError(engine) {
  clearSqlErrorMark();
  const response = sqlStateFor(engine).response;
  if (!sqlEditor || !response?.error) return;
  const statement = response.statements[response.error.index]?.sql;
  const offset = statement ? sqlEditor.getValue().indexOf(statement) : -1;
  if (offset < 0) return;
  const from = sqlEditor.posFromIndex(offset);
  sqlErrorMark = sqlEditor.markText(from, sqlEditor.posFromIndex(offset + statement.length), { className: 'cm-sql-error', title: response.error.message });
  sqlEditor.scrollIntoView(from, 60);
}

function bindSqlActions(body, engine, table) {
  const state = sqlStateFor(engine);
  body.querySelector('#sql-run').addEventListener('click', () => runSql(body, engine, table));
  body.querySelector('#sql-format').addEventListener('click', () => formatSql(engine));
  body.querySelector('#sql-commit')?.addEventListener('click', () => endTransaction(body, engine, table, 'commit'));
  body.querySelector('#sql-rollback')?.addEventListener('click', () => endTransaction(body, engine, table, 'rollback'));
  body.querySelector('#sql-confirm')?.addEventListener('click', () => runSql(body, engine, table, true));
  body.querySelector('#sql-cancel')?.addEventListener('click', () => { state.confirmations = null; updateSqlConsole(body, engine, table); });
  body.querySelector('#sql-history').addEventListener('change', (event) => {
    const item = loadSqlHistory().filter((entry) => entry.engine === engine)[Number(event.target.value)];
    if (!item) return;
    if (sqlEditor) { sqlEditor.setValue(item.sql); sqlEditor.focus(); } else { const area = $('#sql-editor'); area.value = item.sql; area.dispatchEvent(new Event('input')); }
  });
  body.querySelector('#sql-csv')?.addEventListener('click', () => downloadCsv(engine));
  bindResultControls(body, engine);
}

async function runSql(body, engine, table, confirmed = false) {
  const state = sqlStateFor(engine);
  if (state.running) return;
  // Confirmation re-runs exactly what was asked the first time.
  const text = confirmed ? state.ranText : (sqlEditor && sqlEditor.somethingSelected() ? sqlEditor.getSelection() : editorText());
  if (!text.trim()) return;
  state.ranText = text;
  state.running = true;
  state.error = null;
  state.confirmations = null;
  updateSqlConsole(body, engine, table);
  try {
    const result = await postJson('api/sql', { engine, sql: text, confirmed });
    if (result.needsConfirmation) state.confirmations = result.confirmations;
    else {
      state.response = result;
      state.views = {};
      sqlTransactions[engine] = result.txOpen;
      rememberSql(engine, text);
      if (result.results.some((item) => item.kind !== 'read')) dataView = null; // the data tab must re-read
    }
  } catch (error) {
    state.error = error.message;
  }
  state.running = false;
  renderTransactionIndicator();
  updateSqlConsole(body, engine, table);
}

async function endTransaction(body, engine, table, action) {
  const state = sqlStateFor(engine);
  try {
    await postJson(`api/sql/${action}`, { engine });
    sqlTransactions[engine] = false;
    dataView = null;
    state.response = null;
    state.error = null;
    state.notice = `Transaction ${action === 'commit' ? 'validée' : 'annulée'}.`;
  } catch (error) {
    state.error = error.message;
  }
  renderTransactionIndicator();
  updateSqlConsole(body, engine, table);
}

function downloadCsv(engine) {
  const state = sqlStateFor(engine);
  const result = [...(state.response?.results || [])].reverse().find((item) => item.columns);
  if (!result) return;
  const rows = visibleRows(result, resultView(state, result.index)); // what is displayed: sorted and filtered
  const cell = (value) => {
    const raw = value && typeof value === 'object' ? (value.binary ? `(binaire ${value.length} octets)` : value.value) : value;
    if (raw === null || raw === undefined) return '';
    const text = String(raw);
    return /[";\n\r]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text;
  };
  const lines = [result.columns.map(cell).join(';'), ...rows.map((row) => row.map(cell).join(';'))];
  const blob = new Blob([`﻿${lines.join('\r\n')}`], { type: 'text/csv;charset=utf-8' });
  const link = document.createElement('a');
  link.href = URL.createObjectURL(blob);
  link.download = `db-atlas-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-')}.csv`;
  link.click();
  setTimeout(() => URL.revokeObjectURL(link.href), 1000);
}
