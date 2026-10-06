/* ccshelf documentation search. Client side only: the index is the generated file
   docs/search-index.js (titles, headings and a short text per section), loaded as a script the first time
   the box is used. Nothing leaves the page. Without this script the search box stays hidden and the
   sidebar remains the way to navigate.

   Keyboard: "/" focuses the box; ArrowDown moves into the results and between them; ArrowUp on the first
   result returns to the box; Enter in the box opens the first result; Escape closes the list. Results
   are plain links, so Tab works too. The result count is announced through a polite live region. */
(function () {
  'use strict';

  var MAX = 12;
  var box, input, panel, list, status;
  var entries = null;     /* flattened index, built on first use */
  var loading = false;
  var waiting = [];
  var failed = false;
  var lastQuery = '';

  function ready(fn) {
    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', fn); else fn();
  }

  function flatten(data) {
    var out = [];
    for (var i = 0; i < data.pages.length; i++) {
      var p = data.pages[i];
      out.push({ url: p.u, page: p.t, head: '', text: p.g, hay: (p.t + ' ' + p.g).toLowerCase(), headLower: '', pageLower: p.t.toLowerCase(), textLower: '' });
      for (var j = 0; j < p.s.length; j++) {
        var s = p.s[j];
        out.push({
          url: p.u + (s[0] ? '#' + s[0] : ''),
          page: p.t,
          head: s[1],
          text: s[2],
          headLower: s[1].toLowerCase(),
          pageLower: p.t.toLowerCase(),
          textLower: s[2].toLowerCase()
        });
      }
    }
    return out;
  }

  function loadIndex(done) {
    if (entries) { done(); return; }
    if (failed) { done(); return; }
    waiting.push(done);
    if (loading) return;
    loading = true;
    var root = box.getAttribute('data-root') || '';
    var s = document.createElement('script');
    s.src = root + 'search-index.js';
    s.onload = function () {
      loading = false;
      var data = window.CCSHELF_DOCS_INDEX;
      if (data && data.pages) entries = flatten(data); else failed = true;
      var cbs = waiting; waiting = [];
      for (var i = 0; i < cbs.length; i++) cbs[i]();
    };
    s.onerror = function () {
      loading = false; failed = true;
      var cbs = waiting; waiting = [];
      for (var i = 0; i < cbs.length; i++) cbs[i]();
    };
    document.head.appendChild(s);
  }

  function tokens(q) {
    var seen = {}, out = [];
    var parts = q.toLowerCase().split(/\s+/);
    for (var i = 0; i < parts.length; i++) {
      if (parts[i] && !seen[parts[i]]) { seen[parts[i]] = true; out.push(parts[i]); }
    }
    return out;
  }

  function search(q) {
    var toks = tokens(q);
    var hits = [];
    if (!toks.length) return hits;
    for (var i = 0; i < entries.length; i++) {
      var e = entries[i], score = 0, ok = true;
      for (var t = 0; t < toks.length; t++) {
        var tok = toks[t], s = 0;
        if (e.headLower.indexOf(tok) === 0) s = 9;
        else if (e.headLower.indexOf(tok) !== -1) s = 6;
        else if (e.pageLower.indexOf(tok) !== -1) s = 3;
        else if (e.textLower.indexOf(tok) !== -1) s = 1;
        if (!s) { ok = false; break; }
        score += s;
      }
      if (ok) hits.push({ e: e, score: score, order: i });
    }
    hits.sort(function (a, b) { return b.score - a.score || a.order - b.order; });
    return hits;
  }

  function highlight(el, text, toks) {
    var lower = text.toLowerCase(), pos = 0;
    while (pos < text.length) {
      var best = -1, len = 0;
      for (var i = 0; i < toks.length; i++) {
        var at = lower.indexOf(toks[i], pos);
        if (at !== -1 && (best === -1 || at < best || (at === best && toks[i].length > len))) { best = at; len = toks[i].length; }
      }
      if (best === -1) break;
      if (best > pos) el.appendChild(document.createTextNode(text.slice(pos, best)));
      var m = document.createElement('mark');
      m.textContent = text.slice(best, best + len);
      el.appendChild(m);
      pos = best + len;
    }
    if (pos < text.length) el.appendChild(document.createTextNode(text.slice(pos)));
  }

  function snippet(text, toks) {
    if (!text) return '';
    var lower = text.toLowerCase(), at = -1;
    for (var i = 0; i < toks.length; i++) {
      var k = lower.indexOf(toks[i]);
      if (k !== -1 && (at === -1 || k < at)) at = k;
    }
    var start = at > 60 ? at - 40 : 0;
    var out = text.slice(start, start + 140);
    return (start > 0 ? '…' : '') + out + (start + 140 < text.length ? '…' : '');
  }

  function clear() {
    while (list.firstChild) list.removeChild(list.firstChild);
  }

  function setOpen(open) {
    panel.hidden = !open;
    input.setAttribute('aria-expanded', open ? 'true' : 'false');
  }

  function close() {
    setOpen(false);
  }

  function render(q) {
    lastQuery = q;
    clear();
    if (!q.trim()) { close(); status.textContent = ''; return; }
    if (failed) {
      var li0 = document.createElement('li');
      li0.className = 'r-none';
      li0.textContent = 'Search is not available here. Use the menu to browse the pages.';
      list.appendChild(li0);
      setOpen(true);
      status.textContent = 'Search is not available.';
      return;
    }
    var toks = tokens(q);
    var hits = search(q);
    if (!hits.length) {
      var li1 = document.createElement('li');
      li1.className = 'r-none';
      li1.textContent = 'No results for “' + q.trim() + '”.';
      list.appendChild(li1);
      setOpen(true);
      status.textContent = 'No results.';
      return;
    }
    var shown = hits.slice(0, MAX);
    for (var i = 0; i < shown.length; i++) {
      var e = shown[i].e;
      var li = document.createElement('li');
      var a = document.createElement('a');
      a.href = (box.getAttribute('data-root') || '') + e.url;
      var pg = document.createElement('span');
      pg.className = 'r-page';
      pg.textContent = e.page;
      a.appendChild(pg);
      var hd = document.createElement('span');
      hd.className = 'r-head';
      highlight(hd, e.head || e.page, toks);
      a.appendChild(hd);
      var sn = snippet(e.text, toks);
      if (sn) {
        var sp = document.createElement('span');
        sp.className = 'r-snip';
        highlight(sp, sn, toks);
        a.appendChild(sp);
      }
      li.appendChild(a);
      list.appendChild(li);
    }
    setOpen(true);
    status.textContent = hits.length + (hits.length === 1 ? ' result' : ' results') + (hits.length > MAX ? ', showing the first ' + MAX : '') + '.';
  }

  function links() { return list.querySelectorAll('a'); }

  ready(function () {
    box = document.getElementById('dsearch');
    input = document.getElementById('docs-q');
    panel = document.getElementById('docs-results');
    list = document.getElementById('docs-list');
    status = document.getElementById('docs-status');
    if (!box || !input || !panel || !list || !status) return;
    box.hidden = false;

    var run = function () {
      var q = input.value;
      loadIndex(function () { if (input.value === q) render(q); });
    };
    input.addEventListener('focus', function () { loadIndex(function () {}); });
    input.addEventListener('input', run);
    input.addEventListener('keydown', function (ev) {
      if (ev.key === 'ArrowDown') {
        var ls = links();
        if (ls.length) { ev.preventDefault(); ls[0].focus(); }
      } else if (ev.key === 'Enter') {
        var first = links()[0];
        if (first) { ev.preventDefault(); window.location.href = first.href; }
      } else if (ev.key === 'Escape') {
        if (!panel.hidden) { ev.preventDefault(); close(); } else if (input.value) { input.value = ''; render(''); }
      }
    });
    list.addEventListener('keydown', function (ev) {
      var ls = Array.prototype.slice.call(links());
      var at = ls.indexOf(document.activeElement);
      if (ev.key === 'ArrowDown') {
        ev.preventDefault();
        if (at < ls.length - 1) ls[at + 1].focus();
      } else if (ev.key === 'ArrowUp') {
        ev.preventDefault();
        if (at > 0) ls[at - 1].focus(); else input.focus();
      } else if (ev.key === 'Home') {
        ev.preventDefault(); ls[0].focus();
      } else if (ev.key === 'End') {
        ev.preventDefault(); ls[ls.length - 1].focus();
      } else if (ev.key === 'Escape') {
        ev.preventDefault(); close(); input.focus();
      }
    });
    box.addEventListener('focusout', function (ev) {
      var to = ev.relatedTarget;
      if (to && box.contains(to)) return;
      if (!to && document.activeElement === document.body) return; /* a click on a result is in flight */
      close();
    });
    document.addEventListener('click', function (ev) {
      if (!box.contains(ev.target)) close();
    });
    input.addEventListener('click', function () {
      if (input.value.trim() && lastQuery === input.value) setOpen(!!list.firstChild);
    });
    document.addEventListener('keydown', function (ev) {
      if (ev.key !== '/' || ev.ctrlKey || ev.metaKey || ev.altKey) return;
      var t = ev.target, tag = t && t.tagName ? t.tagName.toLowerCase() : '';
      if (tag === 'input' || tag === 'textarea' || tag === 'select' || (t && t.isContentEditable)) return;
      ev.preventDefault();
      input.focus();
      input.select();
    });
  });
})();
