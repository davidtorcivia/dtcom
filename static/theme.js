/* Theme bootstrap.
 *
 * Loaded synchronously in <head>, before the stylesheet, so the stored theme
 * is on <html> before the first paint. Running this from the deferred app.js
 * (as an earlier version did) meant a dark-theme visitor saw a white flash on
 * every navigation.
 *
 * Kept deliberately tiny — it blocks rendering — and self-contained so no
 * inline <script> is needed, which lets the Content-Security-Policy forbid
 * inline scripts outright.
 */
(function () {
  var stored;
  try {
    stored = localStorage.getItem('dt_theme');
  } catch (e) {
    // Private mode / storage disabled: fall through to the system preference.
    stored = null;
  }
  var mode = stored === 'light' || stored === 'dark' || stored === 'auto' ? stored : 'auto';
  window.__dtThemeMode = mode;
  if (mode === 'light' || mode === 'dark') {
    document.documentElement.setAttribute('data-theme', mode);
  } else {
    document.documentElement.removeAttribute('data-theme');
  }
})();
