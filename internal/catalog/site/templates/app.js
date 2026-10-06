(function () {
  'use strict';

  // The catalog data is embedded in a JSON script block, so this page also
  // works from file://. All text goes into the page with textContent; no
  // HTML string is ever parsed.
  var data;
  try {
    data = JSON.parse(document.getElementById('catalog-data').textContent);
  } catch (err) {
    data = null;
  }

  var results = document.getElementById('results');
  var countEl = document.getElementById('count');
  var metaEl = document.getElementById('meta');
  var tabsEl = document.getElementById('tabs');
  var qEl = document.getElementById('q');
  var selects = {
    category: document.getElementById('f-category'),
    tag: document.getElementById('f-tag'),
    status: document.getElementById('f-status'),
    owner: document.getElementById('f-owner')
  };

  var ATTRS = /^(aria-[a-z]+|role|title|type|rel|href|target|scope|tabindex)$/;

  function el(tag, opts) {
    var node = document.createElement(tag);
    opts = opts || {};
    if (opts.cls) { node.className = opts.cls; }
    if (opts.text !== undefined && opts.text !== null) { node.textContent = String(opts.text); }
    if (opts.attrs) {
      Object.keys(opts.attrs).forEach(function (k) {
        if (ATTRS.test(k)) { node.setAttribute(k, String(opts.attrs[k])); }
      });
    }
    for (var i = 2; i < arguments.length; i++) {
      if (arguments[i]) { node.appendChild(arguments[i]); }
    }
    return node;
  }

  function clear(node) {
    while (node.firstChild) { node.removeChild(node.firstChild); }
  }

  function str(v) { return typeof v === 'string' ? v : ''; }
  function list(v) { return Array.isArray(v) ? v.filter(function (x) { return typeof x === 'string'; }) : []; }

  // Only absolute http and https links are ever made clickable.
  function safeHref(s) {
    if (typeof s !== 'string' || s.length === 0 || s.length > 2048) { return ''; }
    if (/[\u0000- \u007f]/.test(s)) { return ''; }
    var u;
    try { u = new URL(s); } catch (err) { return ''; }
    if (u.protocol !== 'http:' && u.protocol !== 'https:') { return ''; }
    if (!u.hostname || u.username || u.password) { return ''; }
    return u.href;
  }

  function link(label, href) {
    var safe = safeHref(href);
    if (!safe) { return null; }
    return el('a', { text: label, attrs: { href: safe, rel: 'noopener noreferrer', target: '_blank' } });
  }

  var state = { q: '', category: '', tag: '', status: '', owner: '', view: 'plugins' };
  var VIEWS = [
    { id: 'plugins', label: 'Plugins' },
    { id: 'overlaps', label: 'Overlaps' },
    { id: 'profiles', label: 'Profiles' }
  ];

  function readHash() {
    var h = '';
    try { h = window.location.hash.replace(/^#/, ''); } catch (err) { h = ''; }
    if (!h || h.indexOf('=') < 0) { return; }
    var p;
    try { p = new URLSearchParams(h); } catch (err) { return; }
    Object.keys(state).forEach(function (k) {
      var v = p.get(k);
      if (v !== null) { state[k] = v.slice(0, 200); }
    });
    if (!VIEWS.some(function (v) { return v.id === state.view; })) { state.view = 'plugins'; }
  }

  function writeHash() {
    var p = new URLSearchParams();
    Object.keys(state).forEach(function (k) {
      if (state[k] && !(k === 'view' && state[k] === 'plugins')) { p.set(k, state[k]); }
    });
    var s = p.toString();
    try { window.history.replaceState(null, '', s ? '#' + s : window.location.pathname + window.location.search); } catch (err) { /* not available */ }
  }

  function uniqueSorted(values) {
    var seen = {};
    values.forEach(function (v) { if (v) { seen[v] = true; } });
    return Object.keys(seen).sort();
  }

  function fillSelect(select, values) {
    values.forEach(function (v) {
      var option = el('option', { text: v });
      option.value = v;
      select.appendChild(option);
    });
  }

  if (!data || !Array.isArray(data.plugins)) {
    clear(results);
    results.appendChild(el('p', { cls: 'empty', text: 'The catalog data could not be read.' }));
    return;
  }

  var plugins = data.plugins.map(function (p) {
    var e = {
      name: str(p.name), displayName: str(p.display_name), description: str(p.description), category: str(p.category),
      tags: list(p.tags), version: str(p.version), author: str(p.author), marketplace: str(p.marketplace),
      source: str(p.source), external: p.external === true, homepage: str(p.homepage), repository: str(p.repository),
      license: str(p.license), owner: str(p.owner), status: str(p.status), whenToUse: list(p.when_to_use),
      avoidWhen: list(p.avoid_when), overlapsWith: list(p.overlaps_with), supersededBy: str(p.superseded_by),
      reviewBy: str(p.review_by), support: str(p.support), docs: str(p.docs), hasHooks: p.has_hooks === true,
      hasMCP: p.has_mcp === true, needsReview: p.needs_platform_review === true, skills: p.skills | 0,
      agents: p.agents | 0, commands: p.commands | 0, changed: p.changed_since_tag === true,
      git: (p.git && typeof p.git === 'object') ? p.git : null
    };
    e.hay = [e.name, e.displayName, e.description, e.category, e.tags.join(' '), e.whenToUse.join(' '), e.owner].join('\n').toLowerCase();
    return e;
  });
  var byName = {};
  plugins.forEach(function (p) { byName[p.name] = p; });

  var profiles = (Array.isArray(data.profiles) ? data.profiles : []).map(function (p) {
    return { name: str(p.name), description: str(p.description), owner: str(p.owner), status: str(p.status), whenToUse: list(p.when_to_use) };
  });

  document.title = str(data.title) || document.title;
  var meta = plugins.length + ' plugins';
  if (str(data.generated_at)) { meta += ', generated ' + str(data.generated_at); }
  if (str(data.latest_tag)) { meta += ', latest tag ' + str(data.latest_tag); }
  metaEl.textContent = meta;

  fillSelect(selects.category, uniqueSorted(plugins.map(function (p) { return p.category; })));
  fillSelect(selects.tag, uniqueSorted([].concat.apply([], plugins.map(function (p) { return p.tags; }))));
  fillSelect(selects.status, ['active', 'experimental', 'deprecated']);
  fillSelect(selects.owner, uniqueSorted(plugins.map(function (p) { return p.owner; })));

  function matches(p) {
    if (state.category && p.category !== state.category) { return false; }
    if (state.tag && p.tags.indexOf(state.tag) < 0) { return false; }
    if (state.status && p.status !== state.status) { return false; }
    if (state.owner && p.owner !== state.owner) { return false; }
    var words = state.q.toLowerCase().split(/\s+/).filter(Boolean);
    for (var i = 0; i < words.length; i++) {
      if (p.hay.indexOf(words[i]) < 0) { return false; }
    }
    return true;
  }

  function score(p) {
    var q = state.q.toLowerCase().trim();
    if (!q) { return 0; }
    var n = p.name.toLowerCase();
    if (n === q) { return 100; }
    if (n.indexOf(q) === 0) { return 60; }
    if (n.indexOf(q) >= 0) { return 40; }
    return 10;
  }

  function sorted(items) {
    return items.slice().sort(function (a, b) {
      var da = a.status === 'deprecated' ? 1 : 0;
      var db = b.status === 'deprecated' ? 1 : 0;
      if (da !== db) { return da - db; }
      var sa = score(a), sb = score(b);
      if (sa !== sb) { return sb - sa; }
      return a.name < b.name ? -1 : (a.name > b.name ? 1 : 0);
    });
  }

  function badge(text, cls) {
    return el('li', { cls: 'badge ' + (cls || ''), text: text });
  }

  function setFilter(key, value) {
    state[key] = value;
    syncControls();
    render();
  }

  function chipButton(label, onClick, title) {
    var b = el('button', { cls: 'chip', text: label, attrs: { type: 'button', title: title || '' } });
    b.addEventListener('click', onClick);
    return b;
  }

  function listOf(items) {
    var ul = el('ul');
    items.forEach(function (t) { ul.appendChild(el('li', { text: t })); });
    return ul;
  }

  function fact(dl, term, node) {
    if (!node) { return; }
    dl.appendChild(el('dt', { text: term }));
    dl.appendChild(el('dd', {}, node));
  }

  function renderCard(p) {
    var card = el('li', { cls: 'card' + (p.status === 'deprecated' ? ' deprecated' : '') });
    var title = el('h2', { text: p.displayName || p.name });
    card.appendChild(title);
    if (p.displayName && p.displayName !== p.name) { card.appendChild(el('p', { cls: 'sub', text: p.name })); }

    var badges = el('ul', { cls: 'badges', attrs: { 'aria-label': 'Labels' } });
    if (p.status) { badges.appendChild(badge(p.status, p.status)); }
    if (p.needsReview) { badges.appendChild(badge('needs platform review', 'review')); }
    if (p.external) { badges.appendChild(badge('external source', 'neutral')); }
    if (p.changed) { badges.appendChild(badge('new or changed', 'neutral')); }
    if (p.category) { badges.appendChild(badge(p.category, 'neutral')); }
    if (badges.firstChild) { card.appendChild(badges); }

    if (p.status === 'deprecated') {
      var call = el('p', { cls: 'callout' });
      if (p.supersededBy) {
        call.appendChild(document.createTextNode('Deprecated. Use '));
        var use = el('button', { cls: 'linkish', text: p.supersededBy, attrs: { type: 'button' } });
        use.addEventListener('click', function () { state.q = p.supersededBy; state.status = ''; state.category = ''; state.tag = ''; state.owner = ''; state.view = 'plugins'; syncControls(); render(); });
        call.appendChild(use);
        call.appendChild(document.createTextNode(' instead.'));
      } else {
        call.appendChild(document.createTextNode('Deprecated. No replacement is named.'));
      }
      card.appendChild(call);
    }

    if (p.description) { card.appendChild(el('p', { text: p.description })); }

    var dl = el('dl', { cls: 'facts' });
    fact(dl, 'Owner', p.owner ? el('span', { text: p.owner }) : null);
    if (p.whenToUse.length) { fact(dl, 'Use when', listOf(p.whenToUse)); }
    if (p.avoidWhen.length) { fact(dl, 'Avoid when', listOf(p.avoidWhen)); }
    if (p.overlapsWith.length) {
      var ol = el('ul', { cls: 'chips' });
      p.overlapsWith.forEach(function (n) {
        var li = el('li');
        li.appendChild(chipButton(n, function () { state.q = n; state.view = 'plugins'; syncControls(); render(); }, 'Search for ' + n));
        ol.appendChild(li);
      });
      fact(dl, 'Overlaps with', ol);
    }
    if (p.tags.length) {
      var tl = el('ul', { cls: 'chips' });
      p.tags.forEach(function (t) {
        var li = el('li');
        li.appendChild(chipButton(t, function () { setFilter('tag', t); }, 'Filter by tag ' + t));
        tl.appendChild(li);
      });
      fact(dl, 'Tags', tl);
    }
    if (p.version) { fact(dl, 'Version', el('span', { text: p.version })); }
    if (p.author) { fact(dl, 'Author', el('span', { text: p.author })); }
    var parts = [];
    if (p.skills) { parts.push(p.skills + ' skill' + (p.skills === 1 ? '' : 's')); }
    if (p.agents) { parts.push(p.agents + ' agent' + (p.agents === 1 ? '' : 's')); }
    if (p.commands) { parts.push(p.commands + ' command' + (p.commands === 1 ? '' : 's')); }
    if (p.hasHooks) { parts.push('hooks'); }
    if (p.hasMCP) { parts.push('MCP servers'); }
    if (parts.length) { fact(dl, 'Contains', el('span', { text: parts.join(', ') })); }
    if (p.support) { fact(dl, 'Support', el('span', { text: p.support })); }
    if (p.reviewBy) { fact(dl, 'Review by', el('span', { text: p.reviewBy })); }
    if (p.git && typeof p.git.last_commit === 'string' && p.git.last_commit) {
      var g = 'last change ' + p.git.last_commit;
      if (p.git.authors | 0) { g += ', ' + (p.git.authors | 0) + ' author' + ((p.git.authors | 0) === 1 ? '' : 's'); }
      fact(dl, 'History', el('span', { text: g }));
    }
    if (p.source) { fact(dl, 'Source', el('span', { text: p.source })); }
    var links = el('span');
    [['Docs', p.docs], ['Homepage', p.homepage], ['Repository', p.repository]].forEach(function (pair) {
      var a = link(pair[0], pair[1]);
      if (a) {
        if (links.firstChild) { links.appendChild(document.createTextNode(' · ')); }
        links.appendChild(a);
      }
    });
    if (links.firstChild) { fact(dl, 'Links', links); }
    if (dl.firstChild) { card.appendChild(dl); }
    return card;
  }

  function renderPlugins() {
    var shown = sorted(plugins.filter(matches));
    countEl.textContent = shown.length + ' of ' + plugins.length + ' plugins';
    clear(results);
    if (!shown.length) {
      results.appendChild(el('p', { cls: 'empty', text: 'No plugin matches. Try fewer words or clear the filters.' }));
      return;
    }
    var grid = el('ul', { cls: 'grid', attrs: { 'aria-label': 'Plugins' } });
    shown.forEach(function (p) { grid.appendChild(renderCard(p)); });
    results.appendChild(grid);
  }

  // Overlap clusters: plugins connected through overlaps_with, in both directions.
  function clusters() {
    var parent = {};
    function find(x) { while (parent[x] !== x) { parent[x] = parent[parent[x]]; x = parent[x]; } return x; }
    function union(a, b) { var ra = find(a), rb = find(b); if (ra !== rb) { parent[ra] = rb; } }
    plugins.forEach(function (p) { parent[p.name] = p.name; });
    plugins.forEach(function (p) {
      p.overlapsWith.forEach(function (n) { if (byName[n]) { union(p.name, n); } });
    });
    var groups = {};
    plugins.forEach(function (p) {
      var r = find(p.name);
      (groups[r] = groups[r] || []).push(p);
    });
    return Object.keys(groups).map(function (k) { return groups[k]; })
      .filter(function (g) { return g.length > 1; })
      .map(function (g) { return g.sort(function (a, b) { return a.name < b.name ? -1 : 1; }); })
      .sort(function (a, b) { return a[0].name < b[0].name ? -1 : 1; });
  }

  function renderOverlaps() {
    var groups = clusters().filter(function (g) { return g.some(matches); });
    countEl.textContent = groups.length + ' groups of overlapping plugins';
    clear(results);
    if (!groups.length) {
      results.appendChild(el('p', { cls: 'empty', text: 'No overlapping plugins are declared.' }));
      return;
    }
    groups.forEach(function (g) {
      var section = el('section', { cls: 'cluster' });
      section.appendChild(el('h2', { text: g.map(function (p) { return p.name; }).join(' / ') }));
      var table = el('table');
      var head = el('tr');
      head.appendChild(el('th', { text: 'Compare', attrs: { scope: 'col' } }));
      g.forEach(function (p) { head.appendChild(el('th', { text: p.name, attrs: { scope: 'col' } })); });
      table.appendChild(el('thead', {}, head));
      var body = el('tbody');
      [['Status', 'status'], ['Owner', 'owner'], ['Description', 'description'], ['Use when', 'whenToUse'], ['Avoid when', 'avoidWhen']].forEach(function (row) {
        var tr = el('tr');
        tr.appendChild(el('th', { text: row[0], attrs: { scope: 'row' } }));
        g.forEach(function (p) {
          var td = el('td');
          var v = p[row[1]];
          if (Array.isArray(v)) { if (v.length) { td.appendChild(listOf(v)); } } else { td.textContent = v; }
          tr.appendChild(td);
        });
        body.appendChild(tr);
      });
      table.appendChild(body);
      var scroll = el('div', { cls: 'scroll', attrs: { tabindex: '0', role: 'region', 'aria-label': 'Comparison of ' + g.map(function (p) { return p.name; }).join(', ') } }, table);
      section.appendChild(scroll);
      results.appendChild(section);
    });
  }

  function renderProfiles() {
    var words = state.q.toLowerCase().split(/\s+/).filter(Boolean);
    var shown = profiles.filter(function (p) {
      var hay = [p.name, p.description, p.owner, p.whenToUse.join(' ')].join('\n').toLowerCase();
      return words.every(function (w) { return hay.indexOf(w) >= 0; });
    });
    countEl.textContent = shown.length + ' of ' + profiles.length + ' profiles';
    clear(results);
    if (!shown.length) {
      results.appendChild(el('p', { cls: 'empty', text: profiles.length ? 'No profile matches.' : 'This catalog lists no profiles.' }));
      return;
    }
    var grid = el('ul', { cls: 'grid', attrs: { 'aria-label': 'Profiles' } });
    shown.forEach(function (p) {
      var card = el('li', { cls: 'card' });
      card.appendChild(el('h2', { text: p.name }));
      var badges = el('ul', { cls: 'badges', attrs: { 'aria-label': 'Labels' } });
      if (p.status) { badges.appendChild(badge(p.status, p.status)); }
      if (badges.firstChild) { card.appendChild(badges); }
      if (p.description) { card.appendChild(el('p', { text: p.description })); }
      var dl = el('dl', { cls: 'facts' });
      fact(dl, 'Owner', p.owner ? el('span', { text: p.owner }) : null);
      if (p.whenToUse.length) { fact(dl, 'Use when', listOf(p.whenToUse)); }
      fact(dl, 'Run with', el('span', { text: 'ccshelf run ' + p.name }));
      card.appendChild(dl);
      grid.appendChild(card);
    });
    results.appendChild(grid);
  }

  function renderTabs() {
    clear(tabsEl);
    VIEWS.forEach(function (v) {
      var b = el('button', { text: v.label, attrs: { type: 'button', 'aria-pressed': state.view === v.id ? 'true' : 'false' } });
      b.addEventListener('click', function () { state.view = v.id; render(); });
      tabsEl.appendChild(b);
    });
  }

  function render() {
    renderTabs();
    if (state.view === 'overlaps') { renderOverlaps(); }
    else if (state.view === 'profiles') { renderProfiles(); }
    else { renderPlugins(); }
    writeHash();
  }

  function syncControls() {
    qEl.value = state.q;
    Object.keys(selects).forEach(function (k) {
      selects[k].value = state[k];
      if (selects[k].value !== state[k]) { state[k] = ''; selects[k].value = ''; }
    });
  }

  qEl.addEventListener('input', function () { state.q = qEl.value; render(); });
  Object.keys(selects).forEach(function (k) {
    selects[k].addEventListener('change', function () { state[k] = selects[k].value; render(); });
  });
  document.getElementById('reset').addEventListener('click', function () {
    state.q = ''; state.category = ''; state.tag = ''; state.status = ''; state.owner = '';
    syncControls();
    render();
    qEl.focus();
  });
  document.getElementById('controls').addEventListener('submit', function (ev) { ev.preventDefault(); });
  document.addEventListener('keydown', function (ev) {
    if (ev.key !== '/' || ev.ctrlKey || ev.metaKey || ev.altKey) { return; }
    var t = ev.target && ev.target.tagName;
    if (t === 'INPUT' || t === 'SELECT' || t === 'TEXTAREA') { return; }
    ev.preventDefault();
    qEl.focus();
  });
  window.addEventListener('hashchange', function () { readHash(); syncControls(); render(); });

  readHash();
  syncControls();
  render();
}());
