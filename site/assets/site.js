/* ccshelf website script. No dependencies, no network, no inline handlers.
   Part 1 runs immediately (in <head>) so the saved theme is applied before first paint.
   Part 2 enhances the page after the DOM is ready. Everything works without JS, with fewer niceties. */
(function () {
  'use strict';
  var root = document.documentElement;
  var KEY = 'ccshelf-theme';

  function readTheme() {
    try { var v = window.localStorage.getItem(KEY); return v === 'light' || v === 'dark' ? v : null; } catch (e) { return null; }
  }
  function writeTheme(v) {
    try { window.localStorage.setItem(KEY, v); } catch (e) { /* storage may be blocked */ }
  }
  function systemDark() {
    return !!(window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches);
  }
  function currentIsDark() {
    var t = root.getAttribute('data-theme');
    return t ? t === 'dark' : systemDark();
  }
  var saved = readTheme();
  if (saved) root.setAttribute('data-theme', saved);

  function ready(fn) {
    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', fn); else fn();
  }

  ready(function () {
    var reduce = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;

    /* ---- Repository links: the one REPO_URL is the href of #repo in the footer. ---- */
    var repoLink = document.getElementById('repo');
    if (repoLink) {
      var base = repoLink.getAttribute('href').replace(/\/+$/, '');
      var links = document.querySelectorAll('a[data-repo-path]');
      for (var i = 0; i < links.length; i++) {
        links[i].setAttribute('href', base + '/' + links[i].getAttribute('data-repo-path'));
      }
    }

    /* ---- Theme toggle ---- */
    var toggle = document.getElementById('theme-toggle');
    if (toggle) {
      toggle.hidden = false;
      toggle.setAttribute('aria-pressed', currentIsDark() ? 'true' : 'false');
      toggle.addEventListener('click', function () {
        var next = currentIsDark() ? 'light' : 'dark';
        root.setAttribute('data-theme', next);
        writeTheme(next);
        toggle.setAttribute('aria-pressed', next === 'dark' ? 'true' : 'false');
      });
    }

    /* ---- Loadout demo ---- */
    var PLUGINS = ['audit-logger', 'design-kit', 'docs-writer', 'partner-linter', 'release-notes', 'seo-tools', 'sre-kit'];
    var PROTECTED = { 'audit-logger': true };
    var MCP = ['figma', 'pagerduty-ro'];
    /* frontend, sre and seo mirror examples/org-data-repo/profiles; "writing" is invented for this demo. */
    var PROFILES = {
      frontend: { plugins: ['design-kit', 'docs-writer'], mcp: ['figma'], model: 'opus', effort: 'high', prompt: 'prompts/frontend.md' },
      sre: { plugins: ['sre-kit', 'partner-linter'], mcp: ['pagerduty-ro'], model: '', effort: 'high', prompt: '' },
      seo: { plugins: ['seo-tools', 'docs-writer'], mcp: [], model: '', effort: 'medium', prompt: '' },
      writing: { plugins: ['docs-writer'], mcp: [], model: '', effort: 'medium', prompt: '' }
    };

    var picker = document.getElementById('picker');
    var typedEl = document.getElementById('typed');
    var caret = document.getElementById('caret');
    var outEl = document.getElementById('term-out');
    var jsonEl = document.getElementById('term-json');
    var subEl = document.getElementById('term-sub');
    var summaryEl = document.getElementById('demo-summary');
    var timer = null;

    function setItem(li, state, tagText) {
      li.classList.remove('is-on', 'is-off', 'is-protected');
      li.classList.add(state);
      var tag = li.querySelector('.tag');
      if (tag) tag.textContent = tagText;
    }

    function renderOut(p) {
      var parts = ['claude --settings <cache>/settings-<hash>.json \\', '       --mcp-config <cache>/mcp-<hash>.json \\'];
      if (p.prompt) parts.push('       --append-system-prompt-file ' + p.prompt + ' \\');
      parts.push('       ' + (p.model ? '--model ' + p.model + ' ' : '') + '--effort ' + p.effort);
      var c = document.createElement('span');
      c.className = 'c';
      c.textContent = '# claude is started with:';
      outEl.textContent = '';
      outEl.appendChild(c);
      outEl.appendChild(document.createTextNode('\n' + parts.join('\n')));
    }

    function settingsJSON(name, p) {
      var enabled = {};
      PLUGINS.forEach(function (id) { enabled[id + '@acme'] = !!PROTECTED[id] || p.plugins.indexOf(id) !== -1; });
      var o = { disableClaudeAiConnectors: true, enabledPlugins: enabled, env: { CCSHELF_PROFILE: name } };
      if (p.model) o.model = p.model;
      return JSON.stringify(o, null, 2).replace('"env": {\n    "CCSHELF_PROFILE": "' + name + '"\n  }', '"env": { "CCSHELF_PROFILE": "' + name + '" }');
    }

    function applyProfile(name, animate) {
      var p = PROFILES[name];
      if (!p) return;
      var on = 0, masked = 0, prot = 0;
      PLUGINS.forEach(function (id) {
        var li = document.querySelector('#plugin-items [data-id="' + id + '"]');
        if (!li) return;
        if (PROTECTED[id]) { setItem(li, 'is-protected', 'always on'); prot++; }
        else if (p.plugins.indexOf(id) !== -1) { setItem(li, 'is-on', 'on'); on++; }
        else { setItem(li, 'is-off', 'masked'); masked++; }
      });
      MCP.forEach(function (id) {
        var li = document.querySelector('#mcp-items [data-id="' + id + '"]');
        if (!li) return;
        if (p.mcp.indexOf(id) !== -1) setItem(li, 'is-on', 'on'); else setItem(li, 'is-off', 'not named');
      });
      summaryEl.textContent = name + ': ' + on + (on === 1 ? ' plugin' : ' plugins') + ' on, ' + prot + ' protected ' + (prot === 1 ? 'plugin' : 'plugins') +
        ' kept on, ' + masked + ' masked. ' + p.mcp.length + (p.mcp.length === 1 ? ' MCP server.' : ' MCP servers.');
      subEl.textContent = 'ccshelf dry-run ' + name + ' prints the exact command';
      jsonEl.textContent = settingsJSON(name, p);

      var full = 'ccshelf run ' + name;
      if (timer) { window.clearInterval(timer); timer = null; }
      if (!animate || reduce) {
        typedEl.textContent = full;
        renderOut(p);
        outEl.hidden = false;
        caret.classList.add('done');
        return;
      }
      typedEl.textContent = '';
      outEl.hidden = true;
      caret.classList.remove('done');
      var n = 0;
      timer = window.setInterval(function () {
        n++;
        typedEl.textContent = full.slice(0, n);
        if (n >= full.length) {
          window.clearInterval(timer); timer = null;
          renderOut(p);
          outEl.hidden = false;
          caret.classList.add('done');
        }
      }, 28);
    }

    if (picker && typedEl && outEl && jsonEl && summaryEl && subEl && caret) {
      picker.addEventListener('change', function (ev) {
        var t = ev.target;
        if (t && t.name === 'profile') applyProfile(t.value, true);
      });
      applyProfile('frontend', false);
    }

    /* ---- Catalog filter ---- */
    var tools = document.getElementById('cat-tools');
    var q = document.getElementById('cat-q');
    var entries = document.querySelectorAll('#entries .entry');
    var empty = document.getElementById('cat-empty');
    if (tools && q && entries.length && empty) {
      tools.hidden = false;
      var statusInputs = tools.querySelectorAll('input[name="status"]');
      var filter = function () {
        var needle = q.value.trim().toLowerCase();
        var status = 'all';
        for (var i = 0; i < statusInputs.length; i++) if (statusInputs[i].checked) status = statusInputs[i].value;
        var shown = 0;
        for (var j = 0; j < entries.length; j++) {
          var e = entries[j];
          var ok = (status === 'all' || e.getAttribute('data-status') === status) &&
            (needle === '' || e.textContent.toLowerCase().indexOf(needle) !== -1);
          e.hidden = !ok;
          if (ok) shown++;
        }
        empty.hidden = shown !== 0;
      };
      q.addEventListener('input', filter);
      tools.addEventListener('change', filter);
    }

    /* ---- Copy buttons ---- */
    var buttons = document.querySelectorAll('button.copy[data-copy]');
    for (var b = 0; b < buttons.length; b++) {
      (function (btn) {
        var target = document.getElementById(btn.getAttribute('data-copy'));
        if (!target) return;
        btn.hidden = false;
        btn.parentNode.classList.add('has-copy');
        btn.addEventListener('click', function () {
          var text = target.textContent.replace(/\n$/, '');
          var done = function (msg) {
            btn.textContent = msg;
            window.setTimeout(function () { btn.textContent = 'Copy'; }, 1800);
          };
          var fallback = function () {
            var sel = window.getSelection();
            var range = document.createRange();
            range.selectNodeContents(target);
            sel.removeAllRanges();
            sel.addRange(range);
            done('Selected, press Ctrl+C');
          };
          if (navigator.clipboard && navigator.clipboard.writeText) {
            navigator.clipboard.writeText(text).then(function () { done('Copied'); }, fallback);
          } else {
            fallback();
          }
        });
      })(buttons[b]);
    }
  });
})();
