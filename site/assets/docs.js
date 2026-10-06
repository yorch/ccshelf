/* ccshelf documentation pages: progressive enhancement only. Without this script the sidebar and
   the "On this page" list stay open and every link works. No dependencies, no network, no inline handlers.
   - On narrow screens the sidebar collapses behind a "Documentation menu" button and the table of
     contents of the page folds into its summary.
   - Code blocks that scroll sideways become keyboard focusable, named regions, so they can be
     scrolled with the arrow keys and a screen reader says what they are. */
(function () {
  'use strict';

  function ready(fn) {
    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', fn); else fn();
  }

  ready(function () {
    var mqNav = window.matchMedia ? window.matchMedia('(max-width: 899px)') : null;
    var mqToc = window.matchMedia ? window.matchMedia('(max-width: 1199px)') : null;

    /* ---- Sidebar menu button (narrow screens) ---- */
    var nav = document.getElementById('docnav');
    if (nav && mqNav) {
      var btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'navbtn';
      btn.setAttribute('aria-controls', 'docnav');
      btn.textContent = 'Documentation menu';
      nav.parentNode.insertBefore(btn, nav);
      var setOpen = function (open) {
        btn.setAttribute('aria-expanded', open ? 'true' : 'false');
        if (open) nav.removeAttribute('data-collapsed'); else nav.setAttribute('data-collapsed', '');
      };
      var apply = function () {
        if (mqNav.matches) setOpen(false);
        else { btn.setAttribute('aria-expanded', 'true'); nav.removeAttribute('data-collapsed'); }
      };
      btn.addEventListener('click', function () { setOpen(btn.getAttribute('aria-expanded') !== 'true'); });
      nav.addEventListener('keydown', function (ev) {
        if (ev.key === 'Escape' && mqNav.matches && btn.getAttribute('aria-expanded') === 'true') {
          setOpen(false);
          btn.focus();
        }
      });
      if (mqNav.addEventListener) mqNav.addEventListener('change', apply);
      apply();
    }

    /* ---- "On this page": folded on narrow screens, always open beside the text on wide ones ---- */
    var toc = document.querySelector('.dmain:not(.dmain-wide) details.toc-d');
    if (toc && mqToc) {
      var sum = toc.querySelector('summary');
      var fold = function () {
        if (mqToc.matches) {
          toc.removeAttribute('open');
          if (sum) sum.removeAttribute('tabindex');
        } else {
          toc.setAttribute('open', '');
          if (sum) sum.setAttribute('tabindex', '-1');
        }
      };
      toc.addEventListener('toggle', function () { if (!mqToc.matches && !toc.open) toc.open = true; });
      if (mqToc.addEventListener) mqToc.addEventListener('change', fold);
      fold();
    }

    /* ---- Scrollable regions are reachable from the keyboard ---- */
    var regions = document.querySelectorAll('.doc .cmd pre');
    var mark = function () {
      for (var i = 0; i < regions.length; i++) {
        var r = regions[i];
        if (r.scrollWidth > r.clientWidth + 1) {
          r.setAttribute('tabindex', '0');
          r.setAttribute('role', 'region');
          r.setAttribute('aria-label', 'Code');
        } else {
          r.removeAttribute('tabindex');
          r.removeAttribute('role');
          r.removeAttribute('aria-label');
        }
      }
    };
    if (regions.length) {
      mark();
      window.addEventListener('resize', mark);
    }

  });
})();
