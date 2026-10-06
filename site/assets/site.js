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

    /* ---- Theme toggle: the label states the current theme, and says what a press does. ---- */
    var toggle = document.getElementById('theme-toggle');
    if (toggle) {
      var paint = function () {
        var dark = currentIsDark();
        toggle.textContent = 'Theme: ' + (dark ? 'dark' : 'light');
        toggle.setAttribute('aria-label', 'Theme: ' + (dark ? 'dark' : 'light') + '. Switch to ' + (dark ? 'light' : 'dark') + '.');
      };
      toggle.hidden = false;
      paint();
      toggle.addEventListener('click', function () {
        var next = currentIsDark() ? 'light' : 'dark';
        root.setAttribute('data-theme', next);
        writeTheme(next);
        paint();
      });
      if (window.matchMedia) {
        var mq = window.matchMedia('(prefers-color-scheme: dark)');
        var onChange = function () { if (!root.getAttribute('data-theme')) paint(); };
        if (mq.addEventListener) mq.addEventListener('change', onChange);
      }
    }

    /* ---- Loadout demo. The data is real ccshelf output captured by scripts/regen-site-demo.sh
            (assets/demo-data.js). Without it, or without JS, the page keeps its static frontend example. ---- */
    var DEMO = window.CCSHELF_DEMO;
    var picker = document.getElementById('picker');
    var typedEl = document.getElementById('typed');
    var caret = document.getElementById('caret');
    var outEl = document.getElementById('term-out');
    var warnEl = document.getElementById('term-warn');
    var lineEl = document.getElementById('term-line');
    var jsonEl = document.getElementById('term-json');
    var fileEl = document.getElementById('term-file');
    var srcEl = document.getElementById('term-source');
    var summaryEl = document.getElementById('demo-summary');
    var timer = null;

    function stateOf(li) {
      return li.classList.contains('is-on') ? 'is-on' : li.classList.contains('is-protected') ? 'is-protected' : 'is-off';
    }
    function setItem(li, state, tagText) {
      var before = stateOf(li);
      li.classList.remove('is-on', 'is-off', 'is-protected', 'just-masked');
      li.classList.add(state);
      var tag = li.querySelector('.tag');
      if (tag) tag.textContent = tagText;
      if (state === 'is-off' && before !== 'is-off' && !reduce) {
        void li.offsetWidth; /* restart the animation */
        li.classList.add('just-masked');
        window.setTimeout(function () { li.classList.remove('just-masked'); }, 700);
      }
    }
    function plural(n, one, many) { return n + ' ' + (n === 1 ? one : many); }

    function showOutput(d) {
      warnEl.textContent = d.warnings.join('\n');
      warnEl.hidden = d.warnings.length === 0;
      lineEl.textContent = d.command;
      outEl.hidden = false;
    }

    function applyProfile(name, animate) {
      var d = DEMO.profiles[name];
      if (!d) return;
      var enabled = d.settings.enabledPlugins || {};
      var on = 0, masked = 0, prot = 0;
      var items = document.querySelectorAll('#plugin-items .item');
      for (var i = 0; i < items.length; i++) {
        var li = items[i];
        var key = li.getAttribute('data-id') + '@acme';
        if (enabled[key] === true) { setItem(li, 'is-on', 'on'); on++; }
        else if (enabled[key] === false) { setItem(li, 'is-off', 'masked'); masked++; }
        else { setItem(li, 'is-protected', 'always on'); prot++; } /* left out of enabledPlugins: protected */
      }
      var mcp = document.querySelectorAll('#mcp-items .item');
      for (var k = 0; k < mcp.length; k++) {
        var id = mcp[k].getAttribute('data-id');
        if (id === 'connectors') {
          if (d.settings.disableClaudeAiConnectors) setItem(mcp[k], 'is-off', 'hidden'); else setItem(mcp[k], 'is-on', 'on');
        } else if (d.mcpServers.indexOf(id) !== -1) setItem(mcp[k], 'is-on', 'on');
        else setItem(mcp[k], 'is-off', 'not named');
      }
      summaryEl.textContent = name + ': ' + plural(on, 'plugin', 'plugins') + ' on, ' + plural(prot, 'protected plugin', 'protected plugins') +
        ' kept on, ' + masked + ' masked. ' + plural(d.mcpServers.length, 'MCP server', 'MCP servers') + '.';
      srcEl.textContent = d.source.indexOf('ccshelf ') === 0 ? 'created with: ' + d.source : 'profile: ' + d.source;
      jsonEl.textContent = JSON.stringify(d.settings, null, 2);
      var m = /settings-([0-9a-f]{8})/.exec(d.command);
      fileEl.textContent = m ? 'settings-' + m[1] + '\u2026json' : 'settings file';

      var full = 'ccshelf dry-run ' + name;
      if (timer) { window.clearInterval(timer); timer = null; }
      if (!animate || reduce) {
        typedEl.textContent = full;
        showOutput(d);
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
          showOutput(d);
          caret.classList.add('done');
        }
      }, 24);
    }

    if (DEMO && DEMO.profiles && picker && typedEl && outEl && warnEl && lineEl && jsonEl && fileEl && srcEl && summaryEl && caret) {
      var cap = DEMO.captured || {};
      var capCommit = document.getElementById('cap-commit');
      var capDate = document.getElementById('cap-date');
      if (capCommit && cap.commit) capCommit.textContent = cap.commit;
      if (capDate && cap.date) capDate.textContent = cap.date;
      picker.addEventListener('change', function (ev) {
        var t = ev.target;
        if (t && t.name === 'profile') applyProfile(t.value, true);
      });
      var checked = picker.querySelector('input[name="profile"]:checked');
      applyProfile(checked ? checked.value : 'frontend', false);
    }

    /* ---- Catalog filter, with a polite live count ("3 of 7 plugins"). ---- */
    var tools = document.getElementById('cat-tools');
    var q = document.getElementById('cat-q');
    var entries = document.querySelectorAll('#entries .entry');
    var countEl = document.getElementById('cat-count');
    if (tools && q && entries.length && countEl) {
      tools.hidden = false;
      countEl.hidden = false;
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
        countEl.textContent = shown + ' of ' + entries.length + ' plugins' +
          (shown === 0 ? '. No plugin matches: clear the filter or choose "all".' : '');
        countEl.classList.toggle('zero', shown === 0);
      };
      q.addEventListener('input', filter);
      tools.addEventListener('change', filter);
      filter();
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
