/* Vector SVG Theme Engine (Light -> Dark -> Auto) — CRISP VECTOR SYMBOLS, ZERO EMOJIS */
document.addEventListener('DOMContentLoaded', () => {
  const themeBtn = document.getElementById('theme-toggle-icon');
  let currentTheme = localStorage.getItem('dt_theme') || 'light';

  applyTheme(currentTheme);

  if (themeBtn) {
    themeBtn.addEventListener('click', () => {
      if (currentTheme === 'light') {
        currentTheme = 'dark';
      } else if (currentTheme === 'dark') {
        currentTheme = 'auto';
      } else {
        currentTheme = 'light';
      }
      applyTheme(currentTheme);
    });
  }

  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
    if ((localStorage.getItem('dt_theme') || 'light') === 'auto') {
      applyTheme('auto');
    }
  });

  // === View beacon ===
  // Fires once per page load (not on bots — the server filters those too, but
  // skipping the beacon entirely saves the request). trackPath is set inline in
  // the article template via a <script>window.trackPath = '/posts/foo'</script>.
  if (navigator.userAgent && /bot|crawler|spider|slurp|googlebot|bingbot|duckduckbot|gptbot|claudebot/i.test(navigator.userAgent)) {
    // skip
  } else if (window.trackPath) {
    navigator.sendBeacon('/api/track', JSON.stringify({ path: window.trackPath }));
  }

  // === Search (only runs on /search, where #search-input exists) ===
  var input = document.getElementById('search-input');
  if (input) {
    var results = document.getElementById('search-results');
    var timer;
    input.addEventListener('input', function () {
      clearTimeout(timer);
      timer = setTimeout(function () {
        var q = input.value.trim();
        if (!q) { results.innerHTML = ''; return; }
        fetch('/api/search?q=' + encodeURIComponent(q))
          .then(function (r) { return r.json(); })
          .then(function (hits) {
            if (!hits || !hits.length) {
              results.innerHTML = '<div class="index-row"><span style="color:var(--text-muted)">No results.</span></div>';
              return;
            }
            results.innerHTML = hits.map(function (h) {
              var excerpt = h.Excerpt
                ? '<span style="display:block;color:var(--text-muted);font-size:0.8rem;margin-top:0.25rem">' + h.Excerpt + '</span>'
                : '';
              return '<div class="index-row"><a href="/posts/' + h.Slug + '">' +
                     '<span class="index-date">::</span>' +
                     '<span class="index-title">' + h.Title + '</span>' +
                     '</a>' + excerpt + '</div>';
            }).join('');
          })
          .catch(function () { results.innerHTML = '<div class="index-row"><span style="color:var(--accent)">Search error.</span></div>'; });
      }, 150);
    });
  }
});

function applyTheme(mode) {
  const root = document.documentElement;
  const themeBtn = document.getElementById('theme-toggle-icon');

  root.removeAttribute('data-theme');

  if (mode === 'light' || mode === 'dark') {
    root.setAttribute('data-theme', mode);
    localStorage.setItem('dt_theme', mode);
  } else {
    localStorage.setItem('dt_theme', 'auto');
  }

  if (themeBtn) {
    if (mode === 'light') {
      // Crisp Sun SVG Vector Symbol
      themeBtn.innerHTML = `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="square"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41"/></svg>`;
      themeBtn.setAttribute('title', 'Theme: Light (Click for Dark)');
    } else if (mode === 'dark') {
      // Crisp Moon SVG Vector Symbol
      themeBtn.innerHTML = `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="square"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>`;
      themeBtn.setAttribute('title', 'Theme: Dark (Click for Auto)');
    } else {
      // Crisp System Half-Circle SVG Vector Symbol
      themeBtn.innerHTML = `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="square"><circle cx="12" cy="12" r="9"/><path d="M12 3v18"/><path d="M12 3a9 9 0 0 1 0 18" fill="currentColor"/></svg>`;
      themeBtn.setAttribute('title', 'Theme: Auto System (Click for Light)');
    }
  }
}
