// Atlas Schema Desk — Ctrl K palette: tables, columns and SQL programs, keyboard driven.
// Classic script: shares the global scope with the other public/js files, loaded in order by index.html.

const PALETTE_LIMIT = 60;
let paletteItems = [];
let paletteIndex = 0;

function openPalette(initial = '') {
  const palette = $('#palette');
  palette.hidden = false;
  const input = $('#palette-input');
  input.value = initial;
  input.focus();
  updatePalette();
}

function closePalette() {
  $('#palette').hidden = true;
}

// Score: exact name > name prefix > name contains > qualified contains; shorter names first.
function paletteScore(name, qualified, needle) {
  const lowered = name.toLocaleLowerCase('fr');
  if (lowered === needle) return 0;
  if (lowered.startsWith(needle)) return 1;
  if (lowered.includes(needle)) return 2;
  if (qualified.toLocaleLowerCase('fr').includes(needle)) return 3;
  return -1;
}

function updatePalette() {
  const needle = $('#palette-input').value.trim().toLocaleLowerCase('fr');
  const results = [];
  if (needle) {
    for (const table of all) {
      const score = paletteScore(table.name, `${table.schema}.${table.name}`, needle);
      if (score >= 0) results.push({ kind: 'table', score, label: table.name, detail: `${engineLabel(table.engine)} · ${table.schema} · ${rowsLabel(table) || `${table.columns.length} col.`}`, table });
    }
    for (const program of programsById.values()) {
      const score = paletteScore(program.name, `${program.schema}.${program.name}`, needle);
      if (score >= 0) results.push({ kind: 'program', score: score + 0.5, label: program.name, detail: `${PROGRAM_KINDS[program.kind] || program.kind} · ${engineLabel(programEngine(program.id))} · ${program.schema}`, program });
    }
    if (needle.length >= 2) {
      for (const table of all) {
        for (const column of table.columns) {
          const score = paletteScore(column.name, `${table.name}.${column.name}`, needle);
          if (score >= 0 && score <= 2) results.push({ kind: 'column', score: score + 1, label: `${table.name}.${column.name}`, detail: `${column.type.length > 32 ? `${column.type.slice(0, 30)}…` : column.type} · ${table.schema} · ${engineLabel(table.engine)}`, table, column });
        }
      }
    }
  }
  results.sort((a, b) => a.score - b.score || a.label.length - b.label.length || a.label.localeCompare(b.label, 'fr'));
  paletteItems = results.slice(0, PALETTE_LIMIT);
  paletteIndex = 0;
  const icon = { table: 'TABLE', column: 'COL', program: 'SQL' };
  $('#palette-results').innerHTML = needle
    ? (paletteItems.map((item, index) => `<div class="palette-item${index === 0 ? ' active' : ''}" role="option" id="palette-item-${index}" data-index="${index}" aria-selected="${index === 0}"><span class="palette-kind ${item.kind}">${icon[item.kind]}</span><span class="palette-label mono">${esc(item.label)}</span><span class="palette-detail">${esc(item.detail)}</span></div>`).join('')
      || '<div class="palette-empty">Aucun résultat.</div>')
      + (results.length > PALETTE_LIMIT ? `<div class="palette-empty">${results.length - PALETTE_LIMIT} autres résultats : précise la recherche.</div>` : '')
    : '<div class="palette-empty">Tape un nom de table, de colonne (2 lettres minimum) ou de programme SQL.</div>';
}

function movePalette(delta) {
  if (!paletteItems.length) return;
  paletteIndex = (paletteIndex + delta + paletteItems.length) % paletteItems.length;
  document.querySelectorAll('.palette-item').forEach((element, index) => {
    element.classList.toggle('active', index === paletteIndex);
    element.setAttribute('aria-selected', String(index === paletteIndex));
  });
  document.querySelector(`#palette-item-${paletteIndex}`)?.scrollIntoView({ block: 'nearest' });
}

function choosePalette(index = paletteIndex) {
  const item = paletteItems[index];
  if (!item) return;
  closePalette();
  if (item.kind === 'table') openTablePage(item.table);
  else if (item.kind === 'column') openTablePage(item.table, 'colonnes', { col: item.column.name });
  else openProgram(item.program.id);
}

function bindPalette() {
  $('#palette-open').addEventListener('click', () => openPalette());
  $('#palette-input').addEventListener('input', updatePalette);
  $('#palette-input').addEventListener('keydown', (event) => {
    if (event.key === 'ArrowDown') { event.preventDefault(); movePalette(1); }
    else if (event.key === 'ArrowUp') { event.preventDefault(); movePalette(-1); }
    else if (event.key === 'Enter') { event.preventDefault(); choosePalette(); }
    else if (event.key === 'Escape') { event.preventDefault(); closePalette(); }
  });
  $('#palette-results').addEventListener('click', (event) => {
    const item = event.target.closest('[data-index]');
    if (item) choosePalette(Number(item.dataset.index));
  });
  $('#palette').addEventListener('click', (event) => { if (event.target.id === 'palette') closePalette(); });
}
