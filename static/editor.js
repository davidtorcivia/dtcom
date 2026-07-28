// Admin post editor: Write/Preview toggle (Obsidian-style, not dual-pane),
// image upload by button, paste, or drag-and-drop, and formatting shortcuts
// (Ctrl/Cmd+B, I, E, K, Shift+X, Shift+H).
(function () {
  'use strict';

  document.addEventListener('DOMContentLoaded', function () {
    // Anchor on the editor itself. Selecting document.querySelector('form')
    // found the logout form in the admin header — the first <form> on the
    // page — so the toggle silently never bound.
    var editor = document.querySelector('.editor');
    if (!editor) {
      return;
    }
    var ta = editor.querySelector('.editor-write');
    var pv = editor.querySelector('.editor-preview');
    var btnWrite = editor.querySelector('[data-mode="write"]');
    var btnPreview = editor.querySelector('[data-mode="preview"]');
    var btnImage = editor.querySelector('[data-action="image"]');
    var fileInput = editor.querySelector('.editor-file');
    var statusEl = editor.querySelector('.editor-status');
    if (!ta || !pv || !btnWrite || !btnPreview) {
      return;
    }

    function setStatus(text, isError) {
      if (!statusEl) {
        return;
      }
      statusEl.textContent = text || '';
      statusEl.classList.toggle('is-error', !!isError);
    }

    // --- Write / Preview -----------------------------------------------

    function setMode(mode) {
      var preview = mode === 'preview';
      btnPreview.classList.toggle('active', preview);
      btnWrite.classList.toggle('active', !preview);
      btnPreview.setAttribute('aria-pressed', String(preview));
      btnWrite.setAttribute('aria-pressed', String(!preview));
      ta.hidden = preview;
      pv.hidden = !preview;
      if (!preview) {
        ta.focus();
        return;
      }
      pv.textContent = 'Loading…';
      var fd = new FormData();
      fd.append('body', ta.value);
      fetch('/admin/posts/preview', { method: 'POST', body: fd, credentials: 'same-origin' })
        .then(function (r) {
          if (!r.ok) {
            throw new Error('preview failed: ' + r.status);
          }
          return r.text();
        })
        .then(function (html) {
          // The response is this server's own rendering of the body the author
          // just typed, in the author-only admin origin.
          pv.innerHTML = html;
        })
        .catch(function () {
          pv.textContent = 'Preview failed. Your draft is unaffected — switch back to Write.';
        });
    }

    btnWrite.addEventListener('click', function () { setMode('write'); });
    btnPreview.addEventListener('click', function () { setMode('preview'); });

    // --- Image upload ---------------------------------------------------

    // insertAtCursor keeps the undo stack intact where the browser supports
    // execCommand, and falls back to a direct splice where it doesn't.
    function insertAtCursor(text) {
      ta.focus();
      var ok = false;
      try {
        ok = document.execCommand && document.execCommand('insertText', false, text);
      } catch (e) {
        ok = false;
      }
      if (!ok) {
        var start = ta.selectionStart || 0;
        var end = ta.selectionEnd || 0;
        ta.value = ta.value.slice(0, start) + text + ta.value.slice(end);
        ta.selectionStart = ta.selectionEnd = start + text.length;
      }
    }

    function upload(file) {
      if (!file || !/^image\//.test(file.type)) {
        return;
      }
      setStatus('Uploading ' + (file.name || 'image') + '…');
      var fd = new FormData();
      fd.append('file', file, file.name || 'upload');
      fetch('/admin/images', { method: 'POST', body: fd, credentials: 'same-origin' })
        .then(function (r) {
          return r.json().then(function (body) {
            if (!r.ok) {
              throw new Error(body && body.error ? body.error : 'upload failed');
            }
            return body;
          });
        })
        .then(function (body) {
          insertAtCursor(body.markdown || '![](' + body.url + ')');
          setStatus('Inserted ' + body.url);
        })
        .catch(function (err) {
          setStatus('Upload failed: ' + err.message, true);
        });
    }

    if (btnImage && fileInput) {
      btnImage.addEventListener('click', function () { fileInput.click(); });
      fileInput.addEventListener('change', function () {
        Array.prototype.forEach.call(fileInput.files || [], upload);
        fileInput.value = ''; // allow re-picking the same file
      });
    }

    ta.addEventListener('paste', function (e) {
      var items = (e.clipboardData && e.clipboardData.items) || [];
      var images = Array.prototype.filter.call(items, function (item) {
        return item.kind === 'file' && /^image\//.test(item.type);
      });
      if (!images.length) {
        return;
      }
      e.preventDefault();
      images.forEach(function (item) { upload(item.getAsFile()); });
    });

    ['dragenter', 'dragover'].forEach(function (name) {
      ta.addEventListener(name, function (e) {
        if (e.dataTransfer && Array.prototype.indexOf.call(e.dataTransfer.types || [], 'Files') !== -1) {
          e.preventDefault();
          ta.classList.add('is-dragover');
        }
      });
    });
    ['dragleave', 'drop'].forEach(function (name) {
      ta.addEventListener(name, function () { ta.classList.remove('is-dragover'); });
    });
    ta.addEventListener('drop', function (e) {
      var files = (e.dataTransfer && e.dataTransfer.files) || [];
      if (!files.length) {
        return;
      }
      e.preventDefault();
      Array.prototype.forEach.call(files, upload);
    });

    // --- Draft safety ---------------------------------------------------

    // Warn before navigating away from unsaved edits. Losing a long post to a
    // stray back-button press is the kind of thing that only has to happen
    // once.
    var initial = ta.value;
    var saving = false;
    var form = ta.form;
    if (form) {
      form.addEventListener('submit', function () { saving = true; });
    }
    window.addEventListener('beforeunload', function (e) {
      if (!saving && ta.value !== initial) {
        e.preventDefault();
        e.returnValue = '';
      }
    });

    // --- Formatting shortcuts -------------------------------------------

    // replaceRange swaps [from,to) for text, going through execCommand so the
    // browser's own undo stack records it. Ctrl+Z after a shortcut should undo
    // the formatting, not the last thing typed before it — assigning to
    // ta.value directly wipes the stack outright.
    function replaceRange(from, to, text) {
      ta.focus();
      ta.selectionStart = from;
      ta.selectionEnd = to;
      var ok = false;
      try {
        ok = document.execCommand && document.execCommand('insertText', false, text);
      } catch (e) {
        ok = false;
      }
      if (!ok) {
        ta.value = ta.value.slice(0, from) + text + ta.value.slice(to);
      }
    }

    // wrapSelection toggles a pair of markers around the selection.
    //
    // Toggling matters more than it sounds: without it a second Ctrl+B on
    // already-bold text produces ****text****, which markdown renders as
    // literal asterisks. The markers are recognised both inside the selection
    // (the user selected them too) and immediately outside it (the common case
    // after the first press, which leaves the inner text selected).
    function wrapSelection(before, after, placeholder) {
      var start = ta.selectionStart;
      var end = ta.selectionEnd;
      var value = ta.value;
      var sel = value.slice(start, end);

      var inner = null;
      var from = start;
      var to = end;
      if (sel.length >= before.length + after.length &&
          sel.slice(0, before.length) === before &&
          sel.slice(sel.length - after.length) === after) {
        inner = sel.slice(before.length, sel.length - after.length);
      } else if (start >= before.length &&
                 value.slice(start - before.length, start) === before &&
                 value.slice(end, end + after.length) === after) {
        inner = sel;
        from = start - before.length;
        to = end + after.length;
      }

      if (inner !== null) {
        replaceRange(from, to, inner);
        ta.selectionStart = from;
        ta.selectionEnd = from + inner.length;
        return;
      }

      var body = sel || placeholder || '';
      replaceRange(start, end, before + body + after);
      if (sel) {
        // Keep the text selected so the shortcut can be pressed again to
        // remove it, and so a second format can be layered on top.
        ta.selectionStart = start + before.length;
        ta.selectionEnd = start + before.length + body.length;
      } else if (placeholder) {
        // Select the placeholder so typing replaces it.
        ta.selectionStart = start + before.length;
        ta.selectionEnd = start + before.length + placeholder.length;
      } else {
        ta.selectionStart = ta.selectionEnd = start + before.length;
      }
    }

    // A link is not a symmetric wrap: the selected text becomes the label and
    // the cursor belongs in the empty parentheses, ready for the URL.
    function insertLink() {
      var start = ta.selectionStart;
      var end = ta.selectionEnd;
      var sel = ta.value.slice(start, end);
      var label = sel || 'text';
      replaceRange(start, end, '[' + label + ']()');
      var caret = start + label.length + 3; // [ + label + ](
      ta.selectionStart = ta.selectionEnd = caret;
    }

    // Keyed by the physical letter, lowercased, since Shift changes e.key.
    var WRAPS = {
      b: ['**', '**', 'bold'],
      i: ['*', '*', 'italic'],
      e: ['`', '`', 'code'],
    };
    var SHIFT_WRAPS = {
      x: ['~~', '~~', 'strikethrough'],
      h: ['==', '==', 'highlight'],
    };

    ta.addEventListener('keydown', function (e) {
      if (!(e.ctrlKey || e.metaKey) || e.altKey) {
        return;
      }
      var key = (e.key || '').toLowerCase();
      var spec = e.shiftKey ? SHIFT_WRAPS[key] : WRAPS[key];
      if (spec) {
        e.preventDefault();
        wrapSelection(spec[0], spec[1], spec[2]);
        return;
      }
      if (key === 'k' && !e.shiftKey) {
        e.preventDefault();
        insertLink();
      }
    });

    // Ctrl/Cmd+S saves rather than opening the browser's save dialog.
    document.addEventListener('keydown', function (e) {
      if ((e.ctrlKey || e.metaKey) && e.key === 's' && form) {
        e.preventDefault();
        saving = true;
        form.requestSubmit ? form.requestSubmit() : form.submit();
      }
    });
  });
})();
