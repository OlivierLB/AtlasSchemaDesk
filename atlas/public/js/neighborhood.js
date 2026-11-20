// Atlas Schema Desk — Neighborhood diagram: parents (left) → table (center) → children (right), at 100 %.
// Classic script: shares the global scope with the other public/js files, loaded in order by index.html.

const HOOD_LIMIT = 12;
const hoodExpanded = new Set(); // "<table key>|parents" / "|children"

function groupLinks(entries) {
  // entries: [{ table, label }] → one card per table with all its linking columns
  const byKey = new Map();
  for (const { table, label } of entries) {
    const key = keyOf(table);
    if (!byKey.has(key)) byKey.set(key, { table, labels: [] });
    byKey.get(key).labels.push(label);
  }
  return [...byKey.values()].sort((a, b) => a.table.name.localeCompare(b.table.name, 'fr'));
}

function hoodCard(item, side, center) {
  const sameSchema = item.table.schema === center.schema;
  const name = sameSchema ? item.table.name : `${item.table.schema}.${item.table.name}`;
  return `<a class="hood-card ${side}" href="${esc(tableHref(item.table, 'voisinage'))}" data-hood-key="${esc(keyOf(item.table))}" title="Ouvrir le voisinage de ${esc(item.table.schema)}.${esc(item.table.name)}">
    <span class="hood-name mono">${esc(name)}</span>
    ${item.labels.slice(0, 3).map((label) => `<span class="hood-link mono">${esc(label)}</span>`).join('')}${item.labels.length > 3 ? `<span class="hood-link muted">+ ${item.labels.length - 3} colonnes</span>` : ''}
    <span class="hood-rows">${rowsLabel(item.table)}</span></a>`;
}

function hoodColumn(items, side, table, title) {
  const stateKey = `${keyOf(table)}|${side}`;
  const open = hoodExpanded.has(stateKey);
  const shown = open ? items : items.slice(0, HOOD_LIMIT);
  const more = items.length - shown.length;
  return `<div class="hood-col ${side}"><div class="hood-title">${title} <span class="muted">${items.length}</span></div>
    ${shown.map((item) => hoodCard(item, side, table)).join('') || '<p class="muted small">aucune</p>'}
    ${more > 0 ? `<button class="button hood-more" type="button" data-hood-more="${esc(stateKey)}">+ ${more} autres</button>` : ''}
    ${open && items.length > HOOD_LIMIT ? `<button class="button hood-more" type="button" data-hood-more="${esc(stateKey)}">Réduire</button>` : ''}</div>`;
}

function renderNeighborhood(body, table) {
  const self = keyOf(table);
  const parents = groupLinks((table.foreignKeys || []).map((fk) => ({ table: findTable(table.engine, fk.targetSchema, fk.targetTable), label: `${fk.column} → ${fk.targetColumn}` }))
    .filter((entry) => entry.table && keyOf(entry.table) !== self));
  const children = groupLinks(incomingRelations(table).filter(({ source }) => keyOf(source) !== self)
    .map(({ source, fk }) => ({ table: source, label: `${fk.column} → ${fk.targetColumn}` })));
  const selfRef = (table.foreignKeys || []).filter((fk) => fk.targetSchema === table.schema && fk.targetTable === table.name);
  const keyColumns = table.columns.filter((column) => column.primary || (table.foreignKeys || []).some((fk) => fk.column === column.name));
  body.innerHTML = `<p class="muted small">Tables directement liées par une clé étrangère déclarée. À gauche celles vers lesquelles <strong>${esc(table.name)}</strong> pointe, à droite celles qui pointent vers elle. Clique une table pour te déplacer.</p>
    <div class="hood" id="hood">
      <svg class="hood-lines" id="hood-lines" aria-hidden="true"></svg>
      ${hoodColumn(parents, 'parents', table, 'Pointe vers')}
      <div class="hood-col center"><div class="hood-title">&nbsp;</div><div class="hood-card center" id="hood-center">
        <span class="hood-name mono">${esc(table.name)}</span>
        ${keyColumns.slice(0, 8).map((column) => `<span class="hood-link mono">${column.primary ? '<b class="key pk">PK</b> ' : '<b class="key fk">FK</b> '}${esc(column.name)}</span>`).join('')}${keyColumns.length > 8 ? `<span class="hood-link muted">+ ${keyColumns.length - 8} clés</span>` : ''}
        ${selfRef.length ? `<span class="hood-link muted">↻ se référence elle-même (${selfRef.map((fk) => esc(fk.column)).join(', ')})</span>` : ''}
        <span class="hood-rows">${rowsLabel(table)}</span></div></div>
      ${hoodColumn(children, 'children', table, 'Pointée par')}
    </div>`;
  body.querySelectorAll('[data-hood-more]').forEach((button) => button.addEventListener('click', () => {
    const stateKey = button.dataset.hoodMore;
    if (hoodExpanded.has(stateKey)) hoodExpanded.delete(stateKey); else hoodExpanded.add(stateKey);
    renderNeighborhood(body, table);
  }));
  requestAnimationFrame(drawHoodLines);
}

// Curves from each parent card to the center card, and from the center card to each child.
function drawHoodLines() {
  const hood = $('#hood');
  const svg = $('#hood-lines');
  const center = $('#hood-center');
  if (!hood || !svg || !center) return;
  const origin = hood.getBoundingClientRect();
  svg.setAttribute('width', String(hood.scrollWidth));
  svg.setAttribute('height', String(hood.scrollHeight));
  const box = (element) => {
    const rect = element.getBoundingClientRect();
    return { left: rect.left - origin.left, right: rect.right - origin.left, middle: rect.top - origin.top + Math.min(rect.height / 2, 18) };
  };
  const c = box(center);
  const curve = (x1, y1, x2, y2) => {
    const dx = Math.max(30, (x2 - x1) / 2);
    return `<path d="M${x1},${y1} C${x1 + dx},${y1} ${x2 - dx},${y2} ${x2},${y2}"/>`;
  };
  const paths = [];
  hood.querySelectorAll('.hood-card.parents').forEach((card) => { const p = box(card); paths.push(curve(p.right, p.middle, c.left, c.middle)); });
  hood.querySelectorAll('.hood-card.children').forEach((card) => { const k = box(card); paths.push(curve(c.right, c.middle, k.left, k.middle)); });
  svg.innerHTML = paths.join('');
}
