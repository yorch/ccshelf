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

    function setItem(li, state, tagText) {
      li.classList.remove('is-on', 'is-off', 'is-protected');
      li.classList.add(state);
      var tag = li.querySelector('.tag');
      if (tag) tag.textContent = tagText;
      /* The lift (and the settle) is a CSS transition on the state class; nothing to restart here.
         With reduced motion the stylesheet has no transition, so the state changes at once. */
    }
    function plural(n, one, many) { return n + ' ' + (n === 1 ? one : many); }

    function showOutput(d) {
      warnEl.textContent = d.warnings.join('\n');
      warnEl.hidden = d.warnings.length === 0;
      lineEl.textContent = d.command;
      outEl.classList.remove('is-pending');
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
      outEl.classList.add('is-pending'); /* keeps its space: no layout shift while typing */
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
        if (t && t.name === 'profile') { root.classList.remove('will-play'); applyProfile(t.value, true); }
      });
      var checked = picker.querySelector('input[name="profile"]:checked');
      var first = checked ? checked.value : 'frontend';
      /* Play once on load: type the command, then lift the spines. Not with reduced motion, not when
         the visitor arrived at an anchor or already scrolled, and never again after that. */
      var autoplay = !reduce && !window.location.hash && (window.pageYOffset || 0) < 80;
      if (autoplay) {
        root.classList.add('will-play');
        applyProfile(first, true);
        window.setTimeout(function () {
          root.classList.remove('will-play');
        }, 450);
      } else {
        applyProfile(first, false);
      }
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
    // Each button shows an icon and keeps its text for screen readers; CSS puts it beside the
    // code, never over it, and one shared status line announces the result. The icons are
    // static markup (no page data); the HTML parser puts <svg> in the SVG namespace.
    var icon = function (cls, body) {
      var box = document.createElement('span');
      box.innerHTML = '<svg class="copy-ico ' + cls + '" viewBox="0 0 24 24" width="18" height="18" ' +
        'aria-hidden="true" focusable="false">' + body + '</svg>';
      return box.firstChild;
    };
    var buttons = document.querySelectorAll('button.copy[data-copy]');
    var status = null;
    if (buttons.length) {
      status = document.createElement('p');
      status.className = 'sr-only';
      status.setAttribute('role', 'status');
      document.body.appendChild(status);
    }
    for (var b = 0; b < buttons.length; b++) {
      (function (btn) {
        var target = document.getElementById(btn.getAttribute('data-copy'));
        if (!target) return;
        var label = btn.textContent.replace(/\s+/g, ' ').trim() || 'Copy';
        var text = document.createElement('span');
        text.className = 'sr-only';
        text.textContent = label;
        btn.textContent = '';
        btn.appendChild(icon('ico-copy', '<rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V6a2 2 0 0 1 2-2h8"/>'));
        btn.appendChild(icon('ico-done', '<path d="M5 12.5l4.5 4.5L19 7.5"/>'));
        btn.appendChild(text);
        btn.title = label;
        btn.hidden = false;
        btn.parentNode.classList.add('has-copy');
        var timer = 0;
        btn.addEventListener('click', function () {
          var done = function (msg, ok) {
            btn.classList.toggle('copied', ok);
            btn.title = msg;
            status.textContent = msg;
            window.clearTimeout(timer);
            timer = window.setTimeout(function () {
              btn.classList.remove('copied');
              btn.title = label;
              status.textContent = '';
            }, 1800);
          };
          var fallback = function () {
            var sel = window.getSelection();
            var range = document.createRange();
            range.selectNodeContents(target);
            sel.removeAllRanges();
            sel.addRange(range);
            done('Selected, press Ctrl+C', false);
          };
          if (navigator.clipboard && navigator.clipboard.writeText) {
            navigator.clipboard.writeText(target.textContent.replace(/\n$/, '')).then(function () { done('Copied', true); }, fallback);
          } else {
            fallback();
          }
        });
      })(buttons[b]);
    }
  });
})();
