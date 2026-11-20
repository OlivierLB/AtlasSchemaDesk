// Atlas Schema Desk — Sidebar tree: bases → schemas → tables, live filter, recent tables.
// Classic script: shares the global scope with the other public/js files, loaded in order by index.html.

const expandedNodes = new Set(); // "engine" or "engine\u0000schema"
const TREE_FILTER_LIMIT = 300;
let lastExpandedLocation = null;

function treeFilterText() {
  return $('#tree-filter').value.trim().toLocaleLowerCase('fr');
}

function tableLink(table, { qualified = false } = {}) {
  const active = route.view === 'table' && route.engine === table.engine && route.schema === table.schema && route.table === table.name;
  const triggers = tableImpact(table)?.triggers.length || 0;
  const label = qualified ? `${table.schema}.${table.name}` : table.name;
  return `<a class="tree-table${active ? ' active' : ''}" href="${esc(routeHash({ view: 'table', engine: table.engine, schema: table.schema, table: table.name, tab: route.view === 'table' ? route.tab : 'colonnes' }))}" title="${esc(table.schema)}.${esc(table.name)}"${active ? ' aria-current="page"' : ''}><span class="tree-name">${esc(label)}</span>${triggers ? '<span class="tree-flag" title="Triggers">⚡</span>' : ''}</a>`;
}

function renderSidebar() {
  const filter = treeFilterText();
  const tree = $('#tree');
  if (filter) {
    const matches = all.filter((table) => `${table.schema}.${table.name}`.toLocaleLowerCase('fr').includes(filter))
      .sort((a, b) => a.name.localeCompare(b.name, 'fr'));
    const shown = matches.slice(0, TREE_FILTER_LIMIT);
    tree.innerHTML = `<div class="tree-heading">${matches.length} table${matches.length > 1 ? 's' : ''}${matches.length > shown.length ? ` (${shown.length} affichées)` : ''}</div>`
      + ENGINES.map((engine) => {
        const list = shown.filter((table) => table.engine === engine);
        return list.length ? `<div class="tree-group"><div class="tree-engine-label ${engine}">${esc(engineLabel(engine))}</div>${list.map((table) => tableLink(table, { qualified: true })).join('')}</div>` : '';
      }).join('')
      + (matches.length ? '' : '<p class="tree-empty">Aucune table ne correspond. Ctrl K cherche aussi les colonnes et les programmes.</p>');
    return;
  }
  // Expand the current location once per navigation (the user can still collapse it afterwards).
  const here = `${route.engine || ''}\u0000${route.schema || ''}`;
  if (here !== lastExpandedLocation) {
    lastExpandedLocation = here;
    if (route.engine) expandedNodes.add(route.engine);
    if (route.engine && route.schema) expandedNodes.add(`${route.engine}\u0000${route.schema}`);
  }
  const recents = loadRecents().map((item) => findTable(item.engine, item.schema, item.table)).filter(Boolean);
  const recentBlock = recents.length ? `<div class="tree-group"><div class="tree-heading">Récentes</div>${recents.map((table) => tableLink(table, { qualified: true })).join('')}</div>` : '';
  const engines = ENGINES.filter((engine) => (sources[engine] || []).length).map((engine) => {
    const open = expandedNodes.has(engine);
    const schemas = schemasOf(engine);
    const body = open ? schemas.map(({ schema, tables }) => {
      const node = `${engine}\u0000${schema}`;
      const schemaOpen = expandedNodes.has(node);
      const active = route.view === 'schema' && route.engine === engine && route.schema === schema;
      return `<div class="tree-schema-row"><button class="tree-toggle" type="button" data-toggle="${esc(node)}" aria-expanded="${schemaOpen}" aria-label="${schemaOpen ? 'Replier' : 'Déplier'} ${esc(schema)}">${schemaOpen ? '▾' : '▸'}</button><a class="tree-schema${active ? ' active' : ''}" href="${esc(routeHash({ view: 'schema', engine, schema }))}">${esc(schema)}</a><span class="tree-count">${tables.length}</span></div>`
        + (schemaOpen ? `<div class="tree-tables">${tables.map((table) => tableLink(table)).join('')}</div>` : '');
    }).join('') : '';
    return `<div class="tree-group"><button class="tree-engine ${engine}" type="button" data-toggle="${engine}" aria-expanded="${open}">${open ? '▾' : '▸'} ${esc(engineLabel(engine))}<span class="tree-count">${(sources[engine] || []).length}</span></button>${body}</div>`;
  }).join('');
  tree.innerHTML = recentBlock + engines;
}

function bindSidebar() {
  $('#tree').addEventListener('click', (event) => {
    const toggle = event.target.closest('[data-toggle]');
    if (!toggle) return;
    const node = toggle.dataset.toggle;
    if (expandedNodes.has(node)) expandedNodes.delete(node); else expandedNodes.add(node);
    renderSidebar();
  });
  $('#tree-filter').addEventListener('input', renderSidebar);
}
