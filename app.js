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
