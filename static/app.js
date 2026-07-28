/* dtcom front-end: theme toggle, view beacon, and the /search page.
 *
 * The initial theme is applied by theme.js in <head> before first paint; this
 * file only handles the toggle button and everything that can wait for DOM
 * ready.
 */
(function () {
  'use strict';

  var THEMES = ['light', 'dark', 'auto'];

  function storedTheme() {
    if (window.__dtThemeMode && THEMES.indexOf(window.__dtThemeMode) !== -1) {
      return window.__dtThemeMode;
    }
    try {
      var v = localStorage.getItem('dt_theme');
      return THEMES.indexOf(v) !== -1 ? v : 'auto';
    } catch (e) {
      return 'auto';
    }
  }

  var THEME_ICONS = {
    light: {
      title: 'Theme: Light (click for Dark)',
      svg: '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="square" aria-hidden="true"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41"/></svg>'
    },
    dark: {
      title: 'Theme: Dark (click for Auto)',
      svg: '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="square" aria-hidden="true"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>'
    },
    auto: {
      title: 'Theme: Auto / system (click for Light)',
      svg: '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="square" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M12 3v18"/><path d="M12 3a9 9 0 0 1 0 18" fill="currentColor"/></svg>'
    }
  };

  function applyTheme(mode) {
    var root = document.documentElement;
    if (mode === 'light' || mode === 'dark') {
      root.setAttribute('data-theme', mode);
    } else {
      root.removeAttribute('data-theme');
    }
    window.__dtThemeMode = mode;
    try {
      localStorage.setItem('dt_theme', mode);
    } catch (e) {
      // Storage unavailable: the theme still applies for this page view.
    }
    var btn = document.getElementById('theme-toggle-icon');
    if (btn) {
      var icon = THEME_ICONS[mode] || THEME_ICONS.auto;
      // Static markup from the table above — never user input.
      btn.innerHTML = icon.svg;
      btn.setAttribute('title', icon.title);
      btn.setAttribute('aria-label', icon.title);
    }
  }

  function initTheme() {
    applyTheme(storedTheme());

    var btn = document.getElementById('theme-toggle-icon');
    if (btn) {
      btn.addEventListener('click', function () {
        applyTheme(THEMES[(THEMES.indexOf(storedTheme()) + 1) % THEMES.length]);
      });
    }

    var mq = window.matchMedia('(prefers-color-scheme: dark)');
    var onChange = function () {
      if (storedTheme() === 'auto') {
        applyTheme('auto');
      }
    };
    // addEventListener on MediaQueryList is missing in older Safari.
    if (mq.addEventListener) {
      mq.addEventListener('change', onChange);
    } else if (mq.addListener) {
      mq.addListener(onChange);
    }
  }

  // === View beacon ===
  // Fires once per page load. The path comes from a data attribute on <body>
  // so the page needs no inline script (see the CSP in the server middleware).
  function initBeacon() {
    var path = document.body && document.body.getAttribute('data-track-path');
    if (!path || !navigator.sendBeacon) {
      return;
    }
    // The server filters bots too; skipping the request here saves it the work.
    if (/bot|crawler|spider|slurp|googlebot|bingbot|duckduckbot|gptbot|claudebot|headlesschrome/i.test(navigator.userAgent || '')) {
      return;
    }
    try {
      navigator.sendBeacon('/api/track', new Blob([JSON.stringify({ path: path })], { type: 'application/json' }));
    } catch (e) {
      // A blocked beacon must never break the page.
    }
  }

  // === Search (only runs on /search, where #search-input exists) ===
  function initSearch() {
    var input = document.getElementById('search-input');
    if (!input) {
      return;
    }
    var results = document.getElementById('search-results');
    var status = document.getElementById('search-status');
    var timer = null;
    var seq = 0;

    function setStatus(text) {
      if (status) {
        status.textContent = text;
      }
    }

    // Results are built from DOM nodes and textContent rather than an
    // innerHTML string, so a title or slug can never be interpreted as markup.
    // The one exception is the excerpt, which the server escapes before
    // inserting its <mark> tags (see store.SearchArticles).
    // The markup mirrors the server-rendered index rows (symbol cell + title)
    // so the same grid rules apply; .search-row narrows the grid to the two
    // columns a hit actually has, since a search result carries no date.
    function render(hits) {
      results.textContent = '';
      hits.forEach(function (hit) {
        var row = document.createElement('div');
        row.className = 'index-row search-row';

        var link = document.createElement('a');
        link.href = '/posts/' + encodeURIComponent(hit.Slug);

        var symbolCell = document.createElement('span');
        symbolCell.className = 'index-symbol-cell';
        var symbol = document.createElement('span');
        symbol.className = 'index-symbol';
        symbol.setAttribute('aria-hidden', 'true');
        symbol.textContent = '::';
        symbolCell.appendChild(symbol);

        var title = document.createElement('span');
        title.className = 'index-title';
        title.textContent = hit.Title || hit.Slug;

        link.appendChild(symbolCell);
        link.appendChild(title);
        row.appendChild(link);

        if (hit.Excerpt) {
          var excerpt = document.createElement('p');
          excerpt.className = 'index-excerpt';
          // Escaped server-side, with only <mark> restored — see
          // store.SearchArticles.
          excerpt.innerHTML = hit.Excerpt;
          row.appendChild(excerpt);
        }
        results.appendChild(row);
      });
    }

    function run(query) {
      var mine = ++seq;
      setStatus('Searching…');
      fetch('/api/search?q=' + encodeURIComponent(query), { headers: { Accept: 'application/json' } })
        .then(function (r) {
          if (!r.ok) {
            throw new Error('search failed: ' + r.status);
          }
          return r.json();
        })
        .then(function (hits) {
          // Ignore a response a newer keystroke has already superseded;
          // otherwise a slow early request can overwrite fresher results.
          if (mine !== seq) {
            return;
          }
          hits = hits || [];
          render(hits);
          setStatus(hits.length ? hits.length + (hits.length === 1 ? ' result' : ' results') : 'No results.');
        })
        .catch(function () {
          if (mine !== seq) {
            return;
          }
          results.textContent = '';
          setStatus('Search is unavailable right now.');
        });
    }

    input.addEventListener('input', function () {
      clearTimeout(timer);
      var q = input.value.trim();
      if (!q) {
        seq++; // cancel any in-flight response
        results.textContent = '';
        setStatus('');
        return;
      }
      timer = setTimeout(function () {
        run(q);
      }, 150);
    });

    // Enter searches immediately rather than waiting out the debounce.
    input.addEventListener('keydown', function (e) {
      if (e.key === 'Enter') {
        e.preventDefault();
        clearTimeout(timer);
        var q = input.value.trim();
        if (q) {
          run(q);
        }
      }
    });

    // Support /search?q=term so a search can be linked to or bookmarked.
    var initial = new URLSearchParams(window.location.search).get('q');
    if (initial && initial.trim()) {
      input.value = initial;
      run(initial.trim());
    }
    input.focus();
  }

  document.addEventListener('DOMContentLoaded', function () {
    initTheme();
    initBeacon();
    initSearch();
  });
})();
