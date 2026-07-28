// Admin post editor: toggle between Write and Preview modes (Obsidian-style,
// not dual-pane). Preview fetches rendered HTML from /admin/posts/preview.
document.addEventListener('DOMContentLoaded', function () {
  var form = document.querySelector('form');
  if (!form || !form.querySelector('.editor')) return;
  var ta = form.querySelector('.editor-write');
  var pv = form.querySelector('.editor-preview');
  var btnWrite = form.querySelector('[data-mode="write"]');
  var btnPreview = form.querySelector('[data-mode="preview"]');
  if (!ta || !pv || !btnWrite || !btnPreview) return;

  function setMode(mode) {
    if (mode === 'preview') {
      btnPreview.classList.add('active');
      btnWrite.classList.remove('active');
      ta.hidden = true;
      pv.hidden = false;
      pv.innerHTML = '<em style="color:var(--text-muted)">Loading…</em>';
      var fd = new FormData();
      fd.append('body', ta.value);
      fetch('/admin/posts/preview', { method: 'POST', body: fd })
        .then(function (r) { return r.text(); })
        .then(function (html) { pv.innerHTML = html; })
        .catch(function () { pv.innerHTML = '<em style="color:var(--accent)">Preview failed.</em>'; });
    } else {
      btnWrite.classList.add('active');
      btnPreview.classList.remove('active');
      ta.hidden = false;
      pv.hidden = true;
    }
  }

  btnWrite.addEventListener('click', function () { setMode('write'); });
  btnPreview.addEventListener('click', function () { setMode('preview'); });
});
