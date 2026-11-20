// Atlas Schema Desk — Scheduled events view (« ce qui tourne la nuit »).
// Classic script: shares the global scope with the other public/js files, loaded in order by index.html.

const INTERVAL_UNITS = { MINUTE: 'min', HOUR: 'h', DAY: 'jour', WEEK: 'semaine', MONTH: 'mois', SECOND: 's' };

// "toutes les 1 DAY" + starts → "chaque jour à 04:05"
function describeSchedule(event) {
  const time = event.starts ? event.starts.slice(11, 16) : null;
  const match = /^toutes les (\d+) (\w+)$/.exec(event.schedule || '');
  if (!match) return event.schedule || '';
  const [, value, field] = match;
  if (field === 'DAY' && value === '1') return `chaque jour${time ? ` à ${time}` : ''}`;
  if (field === 'WEEK' && value === '1') return `chaque semaine${event.starts ? ` (depuis le ${event.starts.slice(0, 10)} à ${time})` : ''}`;
  const unit = INTERVAL_UNITS[field] || field.toLowerCase();
  return `toutes les ${value} ${unit}${time ? ` (calé sur ${time})` : ''}`;
}

// Sort key: daily events by time of day, then shorter intervals first.
function scheduleOrder(event) {
  const time = event.starts ? event.starts.slice(11, 16) : '99:99';
  const match = /^toutes les (\d+) (\w+)$/.exec(event.schedule || '');
  const minutes = match ? Number(match[1]) * ({ SECOND: 1 / 60, MINUTE: 1, HOUR: 60, DAY: 1440, WEEK: 10080, MONTH: 43200 }[match[2]] || 1e6) : 1e7;
  return `${minutes >= 1440 ? '1' : '0'}${String(Math.round(minutes)).padStart(8, '0')}${time}`;
}

function eventRow(event) {
  const impact = event.impact || {};
  const direct = (impact.writes || []).length;
  const indirect = (impact.indirectWrites || []).length;
  const active = event.status === 'ENABLED';
  return `<tr class="${active ? '' : 'muted-row'}" data-event-id="${esc(event.id)}">
    <td class="name mono"><button class="link-button strong" type="button" data-event-open="${esc(event.id)}">${esc(event.name)}</button></td>
    <td class="event-schedule">${esc(describeSchedule(event))}</td>
    <td>${active ? 'actif' : esc(event.status.toLowerCase())}</td>
    <td class="num">${direct || ''}</td><td class="num">${indirect || ''}</td>
    <td class="muted">${event.lastExecuted ? esc(event.lastExecuted) : 'jamais (local)'}</td>
    <td class="muted">${esc(event.schema)}</td></tr>`;
}

// Page #/events: scheduled MySQL events sorted by frequency (the sheet opens in the inspector).
function renderEventsView(main) {
  const events = [...programsById.values()].filter((program) => program.kind === 'event');
  events.sort((a, b) => scheduleOrder(a).localeCompare(scheduleOrder(b)) || a.name.localeCompare(b.name, 'fr'));
  const active = events.filter((event) => event.status === 'ENABLED').length;
  main.innerHTML = `<div class="page"><header class="page-header"><h1 class="page-title">${events.length} events planifiés</h1>
    <p class="page-subtitle">Ce qui tourne tout seul dans les bases (${active} actifs), du plus fréquent au plus rare. « Tables écrites » : directement, puis via les procédures appelées. « Dernière exécution » est celle de la base locale. Clique un event pour sa fiche et sa chaîne d'appels.</p></header>
    ${events.length ? `<table class="list-table"><thead><tr><th>Event</th><th>Rythme</th><th>État</th><th class="num">Tables écrites</th><th class="num">+ via appels</th><th>Dernière exécution</th><th>Schéma</th></tr></thead><tbody>${events.map(eventRow).join('')}</tbody></table>` : '<p class="muted">Aucun event planifié.</p>'}</div>`;
  main.querySelectorAll('[data-event-id]').forEach((row) => row.addEventListener('click', () => openProgram(row.dataset.eventId)));
}

function toggleEventsView() {
  if (route.view === 'events') history.length > 1 ? history.back() : navigate({ view: 'home' });
  else navigate({ view: 'events' });
}
