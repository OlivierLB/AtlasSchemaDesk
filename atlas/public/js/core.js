// Atlas Schema Desk — Model helpers, URL router and recent tables.
// Classic script: shares the global scope with the other public/js files, loaded in order by index.html.

const $ = (selector) => document.querySelector(selector);
const esc = (value) => String(value ?? '').replace(/[&<>"']/g, (char) => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
}[char]));

let ENGINES = [];
let appConfig = { connections: [], modules: {} };
const connectionOf = (id) => appConfig.connections.find((c) => c.id === id);
const driverOf = (id) => connectionOf(id)?.driver;
const canWrite = (id) => Boolean(connectionOf(id)?.allow_writes);
let sources = {};
let all = [];
let relationCache = null;

// ---- model
function keyOf(table) {
  return `${table.engine}\u0000${table.schema}\u0000${table.name}`;
}

function findByKey(key) {
  return all.find((table) => keyOf(table) === key);
}

function findTable(engine, schema, name) {
  return all.find((table) => table.engine === engine && table.schema === schema && table.name === name);
}

function allTables() {
  return Object.entries(sources).flatMap(([engine, tables]) => (tables || []).map((table) => ({ ...table, engine })));
}

function allRelations() {
  if (relationCache) return relationCache;
  relationCache = [];
  for (const table of all) {
    for (const fk of table.foreignKeys || []) {
      const target = findTable(table.engine, fk.targetSchema, fk.targetTable);
      if (target) relationCache.push({ source: keyOf(table), target: keyOf(target), fk });
    }
  }
  return relationCache;
}

// Foreign keys of other tables pointing to `table`: [{ source, fk }]
function incomingRelations(table) {
  const key = keyOf(table);
  return allRelations().filter((edge) => edge.target === key).map((edge) => ({ source: findByKey(edge.source), fk: edge.fk }));
}

function schemaTables(engine, schema) {
  return all.filter((table) => table.engine === engine && table.schema === schema);
}

function schemasOf(engine) {
  const bySchema = new Map();
  for (const table of all) {
    if (table.engine !== engine) continue;
    if (!bySchema.has(table.schema)) bySchema.set(table.schema, []);
    bySchema.get(table.schema).push(table);
  }
  return [...bySchema.entries()].map(([schema, tables]) => ({ schema, tables: tables.sort((a, b) => a.name.localeCompare(b.name, 'fr')) }))
    .sort((a, b) => a.schema.localeCompare(b.schema, 'fr'));
}

function engineLabel(engine) { return connectionOf(engine)?.label || String(engine || "");
}

function updateStatus(message, state = '') {
  $('#connection-status').textContent = message;
  $('#status-dot').className = `status-dot ${state}`.trim();
}

// ---- router: #/ · #/events · #/<engine>/<schema> · #/<engine>/<schema>/<table>/<tab>?col=value&…
const TABLE_TABS = ['colonnes', 'relations', 'donnees', 'programmes', 'voisinage', 'sql'];
let route = { view: 'home' };

function parseHash(hash = location.hash) {
  const [path, query = ''] = hash.replace(/^#\/?/, '').split('?');
  const parts = path.split('/').filter(Boolean).map(decodeURIComponent);
  const params = Object.fromEntries(new URLSearchParams(query));
  if (parts[0] === 'events') return { view: 'events' };
  if (parts[0] === 'sql') return { view: 'sql', engine: ENGINES.includes(parts[1]) ? parts[1] : ENGINES[0] };
  if (ENGINES.includes(parts[0]) && parts[1] && parts[2]) {
    return { view: 'table', engine: parts[0], schema: parts[1], table: parts[2], tab: TABLE_TABS.includes(parts[3]) ? parts[3] : 'colonnes', params };
  }
  if (ENGINES.includes(parts[0]) && parts[1]) return { view: 'schema', engine: parts[0], schema: parts[1] };
  return { view: 'home' };
}

function routeHash(target) {
  const enc = encodeURIComponent;
  if (target.view === 'events') return '#/events';
  if (target.view === 'sql') return `#/sql/${target.engine || ENGINES[0]}`;
  if (target.view === 'schema') return `#/${target.engine}/${enc(target.schema)}`;
  if (target.view === 'table') {
    const query = new URLSearchParams(Object.entries(target.params || {}).filter(([, value]) => value !== '' && value != null)).toString();
    return `#/${target.engine}/${enc(target.schema)}/${enc(target.table)}/${target.tab || 'colonnes'}${query ? `?${query}` : ''}`;
  }
  return '#/';
}

function navigate(target, { replace = false } = {}) {
  const hash = routeHash(target);
  if (location.hash === hash) { route = parseHash(hash); render(); return; }
  if (replace) { history.replaceState(null, '', hash); route = parseHash(hash); render(); return; }
  location.hash = hash; // hashchange → render()
}

function openTablePage(table, tab = 'colonnes', params = {}) {
  if (table) navigate({ view: 'table', engine: table.engine, schema: table.schema, table: table.name, tab, params });
}

function currentTable() {
  return route.view === 'table' ? findTable(route.engine, route.schema, route.table) : null;
}

// ---- recent tables (per-viewer convenience: browser storage, may be unavailable)
const RECENT_KEY = 'db-atlas-recent-tables';
const RECENT_MAX = 8;

function loadRecents() {
  try { return JSON.parse(localStorage.getItem(RECENT_KEY) || '[]').filter((item) => item && item.engine && item.schema && item.table); } catch { return []; }
}

function rememberTable(table) {
  const item = { engine: table.engine, schema: table.schema, table: table.name };
  const list = [item, ...loadRecents().filter((other) => !(other.engine === item.engine && other.schema === item.schema && other.table === item.table))].slice(0, RECENT_MAX);
  try { localStorage.setItem(RECENT_KEY, JSON.stringify(list)); } catch { /* storage unavailable: no recents */ }
}
