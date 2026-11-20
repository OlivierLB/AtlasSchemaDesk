// Atlas Schema Desk — Pages: home (schemas per base), schema (sortable table list), table (tabs).
// Classic script: shares the global scope with the other public/js files, loaded in order by index.html.

const TAB_LABELS = { colonnes: 'Colonnes', relations: 'Relations', donnees: 'Données', programmes: 'Programmes SQL', voisinage: 'Voisinage', sql: 'SQL' };
let schemaListState = { key: null, filter: '', sort: { column: 'name', dir: 'asc' } };

function renderCrumbs() {
  const parts = [`<a href="#/">Bases</a>`];
  if (route.view === 'events') parts.push('<span>Events planifiés</span>');
  if (route.view === 'sql') parts.push('<span>Console SQL</span>');
  if (route.engine) parts.push(`<span>${esc(engineLabel(route.engine))}</span>`);
  if (route.schema) parts.push(route.view === 'schema' ? `<span>${esc(route.schema)}</span>` : `<a href="${esc(routeHash({ view: 'schema', engine: route.engine, schema: route.schema }))}">${esc(route.schema)}</a>`);
  if (route.view === 'table') parts.push(`<span>${esc(route.table)}</span>`);
  $('#crumbs').innerHTML = parts.join('<span class="crumb-sep">/</span>');
}

function rowsLabel(table) {
  const stats = tableStats(table);
  if (stats?.rows != null) return stats.rows === 0 ? 'vide' : `≈ ${formatRows(stats.rows)}`;
  return stats?.bytes ? '?' : '';
}

function tableMetrics(table) {
  const stats = tableStats(table);
  const impact = tableImpact(table);
  return {
    name: table.name,
    rows: stats?.rows ?? -1,
    bytes: stats?.bytes ?? -1,
    columns: table.columns.length,
    outgoing: (table.foreignKeys || []).length,
    incoming: incomingRelations(table).length,
    triggers: impact?.triggers.length || 0,
    writers: (impact?.writes.length || 0) + (impact?.indirectWrites.length || 0),
  };
}

// ---- home: every schema of every base, with its weight
function renderHome(main) {
  const sections = ENGINES.filter((engine) => (sources[engine] || []).length).map((engine) => {
    const rows = schemasOf(engine).map(({ schema, tables }) => {
      const bytes = tables.reduce((sum, table) => sum + (tableStats(table)?.bytes || 0), 0);
      const triggers = tables.reduce((sum, table) => sum + (tableImpact(table)?.triggers.length || 0), 0);
      const empty = tables.filter((table) => tableStats(table)?.rows === 0).length;
      return `<tr data-href="${esc(routeHash({ view: 'schema', engine, schema }))}"><td class="name"><a href="${esc(routeHash({ view: 'schema', engine, schema }))}">${esc(schema)}</a></td><td class="num">${tables.length}</td><td class="num">${bytes ? formatBytes(bytes) : ''}</td><td class="num">${empty || ''}</td><td class="num">${triggers ? `⚡ ${triggers}` : ''}</td></tr>`;
    }).join('');
    return `<section class="page-section"><h2 class="section-title ${engine}">${esc(engineLabel(engine))} <span class="muted">${(sources[engine] || []).length} tables</span></h2>
      <table class="list-table"><thead><tr><th>Schéma</th><th class="num">Tables</th><th class="num">Taille</th><th class="num">Vides</th><th class="num">Triggers</th></tr></thead><tbody>${rows}</tbody></table></section>`;
  }).join('');
  main.innerHTML = `<div class="page"><header class="page-header"><h1 class="page-title">Bases locales</h1><p class="page-subtitle">Choisis un schéma, ou cherche directement une table avec <kbd>Ctrl K</kbd>.</p></header>${sections || '<p class="muted">Aucun export chargé.</p>'}</div>`;
  bindRowLinks(main);
}

// Whole table rows act as links (the first cell holds a real <a> for keyboard / middle-click).
function bindRowLinks(container) {
  container.querySelectorAll('tr[data-href]').forEach((row) => row.addEventListener('click', (event) => {
    if (event.target.closest('a, button')) return;
    location.hash = row.dataset.href;
  }));
}

// ---- schema: sortable, filterable list of its tables
const SCHEMA_COLUMNS = [
  ['name', 'Table', false], ['rows', 'Lignes ≈', true], ['bytes', 'Taille', true], ['columns', 'Colonnes', true],
  ['outgoing', 'FK sortantes', true], ['incoming', 'FK entrantes', true], ['triggers', 'Triggers', true], ['writers', 'Écrite par', true],
];

function renderSchemaPage(main, engine, schema) {
  const key = `${engine}\u0000${schema}`;
  if (schemaListState.key !== key) schemaListState = { key, filter: '', sort: { column: 'name', dir: 'asc' } };
  const tables = schemaTables(engine, schema);
  if (!tables.length) { main.innerHTML = `<div class="page"><p class="muted">Schéma inconnu : ${esc(schema)}.</p></div>`; return; }
  main.innerHTML = `<div class="page"><header class="page-header"><h1 class="page-title">${esc(schema)}</h1><p class="page-subtitle">${esc(engineLabel(engine))} · ${tables.length} tables. Clique un en-tête pour trier ; « Écrite par » compte les programmes SQL qui modifient la table (directement ou via appels).</p></header>
    <input class="page-filter" id="schema-filter" type="search" placeholder="Filtrer les ${tables.length} tables…" value="${esc(schemaListState.filter)}" aria-label="Filtrer les tables du schéma">
    <table class="list-table sortable" id="schema-table"><thead><tr>${SCHEMA_COLUMNS.map(([column, label, numeric]) => `<th class="${numeric ? 'num' : ''}"><button type="button" data-sort="${column}">${label}<span class="sort-mark"></span></button></th>`).join('')}</tr></thead><tbody></tbody></table></div>`;
  const metrics = tables.map((table) => ({ table, ...tableMetrics(table) }));
  const fill = () => {
    const { filter, sort } = schemaListState;
    const needle = filter.trim().toLocaleLowerCase('fr');
    const list = metrics.filter((item) => !needle || item.name.toLocaleLowerCase('fr').includes(needle));
    list.sort((a, b) => (sort.column === 'name' ? a.name.localeCompare(b.name, 'fr') : a[sort.column] - b[sort.column]) * (sort.dir === 'desc' ? -1 : 1) || a.name.localeCompare(b.name, 'fr'));
    main.querySelector('tbody').innerHTML = list.map((item) => {
      const href = routeHash({ view: 'table', engine, schema, table: item.name, tab: 'colonnes' });
      const stats = tableStats(item.table);
      return `<tr data-href="${esc(href)}"><td class="name"><a href="${esc(href)}">${esc(item.name)}</a></td><td class="num${stats?.rows === 0 ? ' empty' : ''}">${rowsLabel(item.table)}</td><td class="num">${item.bytes > 0 ? formatBytes(item.bytes) : ''}</td><td class="num">${item.columns}</td><td class="num">${item.outgoing || ''}</td><td class="num">${item.incoming || ''}</td><td class="num">${item.triggers ? `⚡ ${item.triggers}` : ''}</td><td class="num">${item.writers || ''}</td></tr>`;
    }).join('') || `<tr><td colspan="${SCHEMA_COLUMNS.length}" class="muted">Aucune table ne correspond.</td></tr>`;
    main.querySelectorAll('[data-sort]').forEach((button) => {
      button.querySelector('.sort-mark').textContent = sort.column === button.dataset.sort ? (sort.dir === 'asc' ? ' ▲' : ' ▼') : '';
    });
    bindRowLinks(main);
  };
  main.querySelector('#schema-filter').addEventListener('input', (event) => { schemaListState.filter = event.target.value; fill(); });
  main.querySelectorAll('[data-sort]').forEach((button) => button.addEventListener('click', () => {
    const column = button.dataset.sort;
    const same = schemaListState.sort.column === column;
    // Numbers sort biggest first on the first click: the interesting tables come up.
    schemaListState.sort = { column, dir: same ? (schemaListState.sort.dir === 'asc' ? 'desc' : 'asc') : (column === 'name' ? 'asc' : 'desc') };
    fill();
  }));
  fill();
}

// ---- table page
function renderTablePage(main, table, tab) {
  rememberTable(table);
  const impact = tableImpact(table);
  const incoming = incomingRelations(table);
  const programCount = impact ? new Set([...impact.triggers, ...impact.writes, ...impact.indirectWrites, ...impact.reads, ...impact.probableWrites, ...impact.probableReads]).size : 0;
  const counts = { colonnes: table.columns.length, relations: (table.foreignKeys || []).length + incoming.length, programmes: programCount };
  const triggers = impact?.triggers.length || 0;
  const tabLink = (name) => `<a class="tab${name === tab ? ' active' : ''}" href="${esc(routeHash({ view: 'table', engine: table.engine, schema: table.schema, table: table.name, tab: name }))}"${name === tab ? ' aria-current="page"' : ''}>${TAB_LABELS[name]}${counts[name] != null ? `<span class="tab-count">${counts[name]}</span>` : ''}</a>`;
  main.classList.toggle('fill', tab === 'donnees');
  main.innerHTML = `<div class="page table-page${tab === 'donnees' ? ' fill' : ''}">
    <header class="page-header table-header">
      <div class="table-kicker">${esc(engineLabel(table.engine))} · ${esc(table.schema)}</div>
      <h1 class="page-title mono">${esc(table.name)}</h1>
      <div class="table-badges">${statsBadge(table)}${triggers ? `<a class="badge trigger" href="${esc(routeHash({ view: 'table', engine: table.engine, schema: table.schema, table: table.name, tab: 'programmes' }))}" title="Triggers sur cette table">⚡ ${triggers} trigger${triggers > 1 ? 's' : ''}</a>` : ''}${dataDateBadge(table.engine)}</div>
    </header>
    <nav class="tabs" aria-label="Sections de la table">${TABLE_TABS.map(tabLink).join('')}</nav>
    <div class="tab-body" id="tab-body"></div>
  </div>`;
  const body = main.querySelector('#tab-body');
  if (tab === 'relations') renderRelationsTab(body, table, incoming);
  else if (tab === 'donnees') renderDataTab(body, table, route.params || {});
  else if (tab === 'programmes') renderProgramsTab(body, table);
  else if (tab === 'voisinage') renderNeighborhood(body, table);
  else if (tab === 'sql') renderSqlConsole(body, table.engine, table);
  else renderColumnsTab(body, table);
}

function statsBadge(table) {
  const stats = tableStats(table);
  if (stats?.rows != null) return `<span class="badge${stats.rows === 0 ? ' empty' : ''}" title="Estimation du catalogue, sans lecture des données">${stats.rows === 0 ? 'vide' : `≈ ${formatRows(stats.rows, true)}`}${stats.bytes ? ` · ${formatBytes(stats.bytes)}` : ''}</span>`;
  return stats?.bytes ? `<span class="badge" title="Table jamais analysée : nombre de lignes inconnu">${formatBytes(stats.bytes)} · lignes inconnues</span>` : '';
}

function tableHref(table, tab) {
  return routeHash({ view: 'table', engine: table.engine, schema: table.schema, table: table.name, tab });
}

function renderColumnsTab(body, table) {
  const fks = new Map((table.foreignKeys || []).map((fk) => [fk.column, fk]));
  const unique = new Set((table.uniqueConstraints || []).filter((columns) => columns.length === 1).map((columns) => columns[0]));
  const focus = route.params?.col;
  body.innerHTML = `<table class="list-table columns-table"><thead><tr><th>Colonne</th><th>Type</th><th>Null</th><th>Défaut</th><th>Clés</th></tr></thead><tbody>${table.columns.map((column) => {
    const fk = fks.get(column.name);
    const target = fk && findTable(table.engine, fk.targetSchema, fk.targetTable);
    const keys = [column.primary ? '<span class="key pk">PK</span>' : '', unique.has(column.name) && !column.primary ? '<span class="key uq">UNIQUE</span>' : '',
      fk ? `<span class="key fk">FK</span> ${target ? `<a href="${esc(tableHref(target, 'colonnes'))}">${esc(fk.targetSchema)}.${esc(fk.targetTable)}.${esc(fk.targetColumn)}</a>` : esc(`${fk.targetSchema}.${fk.targetTable}.${fk.targetColumn}`)}` : ''].filter(Boolean).join(' ');
    return `<tr id="col-${esc(column.name)}" class="${column.name === focus ? 'highlight' : ''}"><td class="name mono${column.primary ? ' pk' : ''}">${esc(column.name)}</td><td class="mono muted type-cell"><span title="${esc(column.type)}">${esc(column.type)}</span></td><td>${column.nullable ? 'oui' : ''}</td><td class="mono muted">${esc(column.default ?? '')}</td><td>${keys}</td></tr>`;
  }).join('')}</tbody></table>${(table.uniqueConstraints || []).some((columns) => columns.length > 1) ? `<p class="muted small">Contraintes uniques composées : ${(table.uniqueConstraints || []).filter((columns) => columns.length > 1).map((columns) => `(${columns.map(esc).join(', ')})`).join(' · ')}</p>` : ''}`;
  if (focus) body.querySelector('.highlight')?.scrollIntoView({ block: 'center' });
}

function relationItem(table, label, detail) {
  return `<a class="relation-item" href="${esc(tableHref(table, 'relations'))}"><span class="relation-table mono">${esc(label)}</span><span class="relation-detail mono">${esc(detail)}</span><span class="relation-rows">${rowsLabel(table)}</span></a>`;
}

function renderRelationsTab(body, table, incoming) {
  const outgoing = (table.foreignKeys || []).map((fk) => ({ fk, target: findTable(table.engine, fk.targetSchema, fk.targetTable) }));
  const out = outgoing.map(({ fk, target }) => (target ? relationItem(target, `${fk.targetSchema}.${fk.targetTable}`, `${fk.column} → ${fk.targetColumn}`) : `<div class="relation-item missing mono">${esc(`${fk.column} → ${fk.targetSchema}.${fk.targetTable}.${fk.targetColumn}`)} (table absente de l'export)</div>`)).join('');
  const inc = incoming.map(({ source, fk }) => relationItem(source, `${source.schema}.${source.name}`, `${fk.column} → ${fk.targetColumn}`)).join('');
  body.innerHTML = `<div class="relations-grid">
    <section><h2 class="section-title">Pointe vers <span class="muted">${outgoing.length} · ses tables parentes</span></h2>${out || '<p class="muted">Aucune clé étrangère déclarée sur cette table.</p>'}</section>
    <section><h2 class="section-title">Pointée par <span class="muted">${incoming.length} · tables qui la référencent</span></h2>${inc || '<p class="muted">Aucune table ne la référence par une clé étrangère déclarée.</p>'}</section>
  </div><p class="muted small">Seules les clés étrangères <strong>déclarées</strong> sont connues : les jointures faites uniquement dans le code n'apparaissent pas. <a href="${esc(tableHref(table, 'voisinage'))}">Voir le voisinage en schéma →</a></p>`;
}

function renderProgramsTab(body, table) {
  body.innerHTML = programsData ? `<div class="programs-tab">${tableProgramsMarkup(table)}</div>` : '<p class="muted">Programmes SQL non chargés (export des programmes absent).</p>';
  bindDetailLinks(body);
}

function renderNotFound(main) {
  main.innerHTML = `<div class="page"><h1 class="page-title">Introuvable</h1><p class="muted">Cette table n'existe pas dans l'export affiché (autre version de schéma ?). <a href="#/">Revenir aux bases</a></p></div>`;
}
