// Atlas Schema Desk — Schema loading, central render, event bindings and startup.
// Classic script: shares the global scope with the other public/js files, loaded in order by index.html.

let loading = false;
let schemaLoaded = false;

// Renders the page for the current route; the inspector (sheets) is rendered separately.
function render() {
  renderSidebar();
  renderCrumbs();
  const main = $('#main');
  main.classList.remove('fill');
  if (!schemaLoaded) {
    main.innerHTML = '<div class="page"><p class="muted">Chargement du schéma…</p></div>';
    return;
  }
  const context = $('#main-context');
  context.textContent = '';
  if (route.view === 'events') renderEventsView(main);
  else if (route.view === 'sql') renderSqlPage(main, route.engine);
  else if (route.view === 'schema') renderSchemaPage(main, route.engine, route.schema);
  else if (route.view === 'table') {
    const table = currentTable();
    if (table) renderTablePage(main, table, route.tab);
    else renderNotFound(main);
  } else renderHome(main);
  $('#events-toggle').classList.toggle('active', route.view === 'events');
  $('#events-toggle').setAttribute('aria-pressed', String(route.view === 'events'));
  renderDetail();
}

async function loadSchema() {
  if (loading) return;
  loading = true;
  $('#refresh').disabled = true;
  $('#load-banner').classList.remove('visible');
  updateStatus('Lecture du schéma…', 'loading');
  try {
    const configuration = await fetch('api/config', { cache: 'no-store' });
    if (!configuration.ok) throw new Error('Configuration unavailable');
    appConfig = await configuration.json();
    ENGINES = appConfig.connections.map((c) => c.id);
    for (const c of appConfig.connections) SQL_DEFAULT_SCHEMA[c.id] = c.default_schema;
    const css = document.querySelector('#connection-colors') || document.head.appendChild(Object.assign(document.createElement('style'), { id: 'connection-colors' }));
    css.textContent = appConfig.connections.map((c) => `.${c.id}, [data-engine="${c.id}"] { --connection-color: ${c.color}; }`).join('\n');
    const response = await fetch(`data/schema.json?t=${Date.now()}`, { cache: 'no-store' });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const data = await response.json();
    schemaLoaded = true;
    latestSources = data.sources || {};
    sources = { ...latestSources };
    selectedVersions = {};
    all = allTables();
    relationCache = null;
    dataView = null;
    route = parseHash();
    render();
    updateEngineSummary();
    $('#meta').textContent = data.generatedAt ? `Export du ${new Date(data.generatedAt).toLocaleString('fr-FR')}` : 'Métadonnées des connexions configurées';
    updateStatus(`${all.length} tables · ${ENGINES.reduce((sum, engine) => sum + schemasOf(engine).length, 0)} schémas`);
    await loadVersionIndex();
    await loadPrograms(); // re-renders with trigger flags, row estimates and programs
  } catch (error) {
    console.error('[Atlas Schema Desk] Chargement du schéma impossible', error);
    sources = {}; all = []; relationCache = null; schemaLoaded = true;
    render();
    $('#engine-summary').textContent = 'Connexions configurées';
    $('#meta').textContent = 'Export local introuvable';
    updateStatus('Schéma non chargé', 'error');
    showLoadBanner('Export introuvable ou illisible. Lance « atlas-schema-desk » puis clique Actualiser pour exporter les schémas configurés.');
  } finally {
    loading = false;
    $('#refresh').disabled = false;
  }
}

window.addEventListener('hashchange', () => {
  route = parseHash();
  if (route.view !== 'table' || route.tab !== 'donnees') pendingTrailStep = null;
  render();
  if (route.view !== 'table' || route.tab !== 'donnees') $('#main').scrollTop = 0;
});
$('#events-toggle').addEventListener('click', toggleEventsView);
$('#sql-open').addEventListener('click', () => navigate({ view: 'sql', engine: route.engine || ENGINES[0] }));
$('#tx-indicator').addEventListener('click', () => navigate({ view: 'sql', engine: $('#tx-indicator').dataset.engine || ENGINES[0] }));
$('#refresh').addEventListener('click', refreshExport);
$('#versions-toggle').addEventListener('click', (event) => { event.stopPropagation(); toggleVersionPanel(); });
document.addEventListener('click', (event) => {
  // composedPath() is fixed at dispatch time, so it still holds the panel when the
  // clicked button was re-rendered (detached) by its own handler.
  if (!$('#versions-panel').hidden && !event.composedPath().some((node) => node.id === 'versions')) toggleVersionPanel(false);
});
window.addEventListener('resize', () => {
  if (route.view === 'table' && route.tab === 'voisinage') requestAnimationFrame(drawHoodLines);
});
window.addEventListener('keydown', (event) => {
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') { event.preventDefault(); openPalette(); return; }
  if (event.key !== 'Escape' || !$('#palette').hidden) return; // the palette handles its own Escape
  if (!$('#versions-panel').hidden) toggleVersionPanel(false);
  else if (selectedProgramId) { selectedProgramId = null; renderDetail(); }
  else if (dataView?.selectedRow != null) closeRowDetail();
});
bindSidebar();
bindPalette();

loadSchema().then(resumeApplyIfRunning).then(refreshTransactionState);
