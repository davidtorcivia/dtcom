// Admin post editor: Write/Preview toggle (Obsidian-style, not dual-pane),
// image upload by button, paste, or drag-and-drop, formatting shortcuts
// (Ctrl/Cmd+B, I, E, K, Shift+X, Shift+H, Alt+1..6), markdown-aware Enter and
// Tab, saving in place without leaving the post, and a copy of the draft in
// localStorage so a crash or an expired session never costs an evening's work.
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
    var btnPair = editor.querySelector('[data-action="pair"]');
    var pairInput = editor.querySelector('.editor-pair-file');
    var statusEl = editor.querySelector('.editor-status');
    var countEl = editor.querySelector('.editor-count');
    var restoreBar = editor.querySelector('.editor-restore');
    if (!ta || !pv || !btnWrite || !btnPreview) {
      return;
    }

    var form = ta.form;

    // The status the editor returns to whenever the draft differs from what is
    // on disk. Named because writeStash has to recognise its own message.
    var UNSAVED = 'Unsaved changes';

    function setStatus(text, isError) {
      if (!statusEl) {
        return;
      }
      statusEl.textContent = text || '';
      statusEl.classList.toggle('is-error', !!isError);
      // Every other status supersedes the save confirmation, including the
      // "Unsaved changes" that the first keystroke after a save produces.
      statusEl.classList.remove('is-saved');
    }

    // --- The draft as a whole ---------------------------------------------

    // The frontmatter fields live outside .editor, in the same form. They are
    // part of the draft: a title typed and then lost to a crash is as annoying
    // as a lost paragraph, and the restore prompt would be wrong if it only
    // compared bodies.
    var FIELDS = ['title', 'date', 'description', 'tags'];
    var fields = {};
    FIELDS.forEach(function (name) {
      fields[name] = form ? form.querySelector('[name="' + name + '"]') : null;
    });
    var draftBox = form ? form.querySelector('[name="draft"]') : null;
    var slugField = form ? form.querySelector('[name="slug"]') : null;

    function snapshot() {
      var snap = { body: ta.value };
      FIELDS.forEach(function (name) {
        snap[name] = fields[name] ? fields[name].value : '';
      });
      snap.draft = !!(draftBox && draftBox.checked);
      return snap;
    }

    // Both sides are built by snapshot(), so the key order matches and the
    // comparison is a string compare rather than a hand-written field walk.
    function same(a, b) {
      return JSON.stringify(a) === JSON.stringify(b);
    }

    function apply(snap) {
      if (typeof snap.body === 'string') {
        ta.value = snap.body;
      }
      FIELDS.forEach(function (name) {
        if (fields[name] && typeof snap[name] === 'string') {
          fields[name].value = snap[name];
        }
      });
      if (draftBox && typeof snap.draft === 'boolean') {
        draftBox.checked = snap.draft;
      }
    }

    // What is on disk, as far as this page knows: the values the server
    // rendered, moved forward on every successful save.
    var saved = snapshot();

    // --- Autosave to localStorage -----------------------------------------
    //
    // Deliberately not to content/posts/. That file is the published article —
    // writing half a sentence into it would put half a sentence on the site,
    // in the feed and in the search index, and would rebuild the whole site to
    // do it. The stash is a copy nobody but this browser reads.

    // Reading localStorage throws outright in a browser configured to block
    // storage, so every use goes through here and the editor works without it;
    // only crash recovery is lost.
    function stash() {
      try {
        return window.localStorage || null;
      } catch (e) {
        return null;
      }
    }

    // Keyed by slug so two posts open in two tabs keep their own drafts. A post
    // that has never been saved has no slug and shares the "new" key — with one
    // author and one browser that collides only between two unsaved new posts,
    // which is a trade for not inventing an id scheme.
    function stashKey() {
      return 'dtcom-draft:' + ((slugField && slugField.value) || 'new');
    }

    function clearStash(key) {
      var ls = stash();
      if (!ls) {
        return;
      }
      try {
        ls.removeItem(key || stashKey());
      } catch (e) {
        // Nothing to do and nothing at risk: the stash is a copy.
      }
    }

    function writeStash() {
      var ls = stash();
      if (!ls) {
        return;
      }
      var snap = snapshot();
      if (same(snap, saved)) {
        clearStash();
        return;
      }
      snap.t = Date.now();
      try {
        ls.setItem(stashKey(), JSON.stringify(snap));
      } catch (e) {
        return; // out of quota; the page still holds the text
      }
      // Only ever upgrade this line's own message. An upload has just written
      // something the author needs to read — which of two files became the
      // dark one, say — and stomping it a second later with a note about
      // autosaving would take the answer away before it was read.
      if (statusEl && statusEl.textContent === UNSAVED) {
        setStatus(UNSAVED + ' — draft kept in this browser');
      }
    }

    var stashTimer = null;
    function stashSoon() {
      if (stashTimer) {
        clearTimeout(stashTimer);
      }
      stashTimer = setTimeout(writeStash, 1000);
    }

    // Offer the stash back when it says something the server did not render.
    // No timestamps are compared against the file's: "differs from what was
    // loaded" is the whole question, and it answers itself.
    function offerRestore() {
      var ls = stash();
      if (!ls || !restoreBar) {
        return;
      }
      var raw;
      try {
        raw = ls.getItem(stashKey());
      } catch (e) {
        return;
      }
      if (!raw) {
        return;
      }
      var stashed;
      try {
        stashed = JSON.parse(raw);
      } catch (e) {
        clearStash();
        return;
      }
      var when = stashed.t;
      delete stashed.t;
      if (same(stashed, saved)) {
        clearStash();
        return;
      }
      var label = restoreBar.querySelector('.editor-restore-text');
      if (label) {
        label.textContent = when
          ? 'Unsaved changes from ' + new Date(when).toLocaleString() + ' are still here.'
          : 'Unsaved changes from an earlier session are still here.';
      }
      restoreBar.hidden = false;
      restoreBar.addEventListener('click', function (e) {
        var action = e.target && e.target.getAttribute && e.target.getAttribute('data-action');
        if (action === 'restore') {
          apply(stashed);
          restoreBar.hidden = true;
          autogrow();
          updateCount();
          setStatus('Restored — not saved yet');
        } else if (action === 'discard') {
          clearStash();
          restoreBar.hidden = true;
        }
      });
    }

    // --- Saving in place ---------------------------------------------------

    var savingNow = false;

    function save() {
      if (!form || savingNow) {
        return;
      }
      savingNow = true;
      setStatus('Saving…');
      // Snapshot before the request, not after: whatever gets typed while it is
      // in flight is genuinely unsaved, and recording it as saved would leave
      // those keystrokes with nothing to recover them from.
      var sent = snapshot();
      fetch(form.action, {
        method: 'POST',
        body: new FormData(form),
        credentials: 'same-origin',
        headers: { Accept: 'application/json' },
      })
        .then(function (r) {
          // A redirect means this save was not answered by a server that knows
          // about saving in place — an expired session bouncing to the login
          // page, or a binary older than this script (static/ is bind-mounted,
          // so the two can be a version apart). fetch follows the redirect and
          // reports a cheerful 200, which would otherwise be read as success,
          // clear the stash, and leave the author certain they had saved.
          if (r.redirected) {
            throw new Error('Session expired — sign in again in another tab, then Save.');
          }
          return r.json().catch(function () { return {}; }).then(function (body) {
            if (r.status === 401) {
              throw new Error('Session expired — sign in again in another tab, then Save.');
            }
            if (!r.ok) {
              throw new Error(body.error || 'save failed: ' + r.status);
            }
            return body;
          });
        })
        .then(function (body) { onSaved(sent, body.slug); })
        .catch(function (err) { setStatus(err.message, true); })
        .then(function () { savingNow = false; });
    }

    function onSaved(sent, slug) {
      // A new post has no slug until the server derives one from the title.
      // Writing it back into the hidden field is what makes the second save an
      // update of the file the first one created — without it the next save
      // takes the create path again and collides with its own output.
      if (slug && slugField && slugField.value !== slug) {
        clearStash(); // drop the "new" entry before the key moves
        slugField.value = slug;
        if (window.history && window.history.replaceState) {
          window.history.replaceState(null, '', '/admin/posts/' + slug + '/edit');
        }
      }
      saved = sent;
      clearStash();
      setStatus('\u2713 Saved ' + new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }));
      if (statusEl) {
        statusEl.classList.add('is-saved');
      }
    }

    if (form) {
      // Saving used to redirect to the post list, so every Ctrl+S threw the
      // author out of the post they were still writing. Without JS the form
      // still posts and still redirects; this is the enhancement.
      form.addEventListener('submit', function (e) {
        e.preventDefault();
        save();
      });
    }

    // --- Word count and height --------------------------------------------

    function updateCount() {
      if (!countEl) {
        return;
      }
      var words = ta.value.match(/\S+/g);
      var n = words ? words.length : 0;
      countEl.textContent = n === 1 ? '1 word' : n + ' words';
    }

    // The textarea was a fixed 400px box scrolling inside a scrolling page, so
    // a long post meant working through a letterbox with two scrollbars.
    // Growing it to its content leaves one scrollbar: the page's.
    //
    // Collapsing to auto to measure is what makes the reflow honest, and it is
    // also what loses the reader's place: for the length of that forced layout
    // the page is short enough that the browser clamps the scroll offset, and
    // putting the height back does not put the offset back. So the offset is
    // carried across by hand.
    function autogrow() {
      if (!ta.style) {
        return;
      }
      var y = window.scrollY;
      ta.style.height = 'auto';
      ta.style.height = ta.scrollHeight + 'px';
      if (window.scrollY !== y) {
        window.scrollTo(0, y);
      }
    }

    function onEdit() {
      setStatus(UNSAVED);
      stashSoon();
    }

    ta.addEventListener('input', function () {
      autogrow();
      updateCount();
      onEdit();
    });
    FIELDS.forEach(function (name) {
      if (fields[name]) {
        fields[name].addEventListener('input', onEdit);
      }
    });
    if (draftBox) {
      draftBox.addEventListener('change', onEdit);
    }

    // --- Write / Preview -----------------------------------------------

    // The two panes are different heights, so a place in the document carries
    // across as a fraction of the page rather than a pixel offset. Switching
    // used to land at the top, which on a long post means finding your place
    // again every time you check the rendering.
    function scrollFraction() {
      var el = document.documentElement;
      if (!el || !window.scrollTo) {
        return -1;
      }
      var max = el.scrollHeight - window.innerHeight;
      return max > 0 ? window.scrollY / max : 0;
    }

    // Restoring has to wait for a frame. Scrolling in the same turn that swaps
    // the panes lands against the old layout — the page is still the height of
    // whichever pane was showing — and the position that comes out is near the
    // top of the new one. A frame later both panes have been laid out and the
    // fraction means what it says.
    function restoreFraction(f) {
      if (f < 0 || !window.scrollTo) {
        return;
      }
      var apply = function () {
        var el = document.documentElement;
        if (!el) {
          return;
        }
        window.scrollTo(0, f * (el.scrollHeight - window.innerHeight));
      };
      if (window.requestAnimationFrame) {
        window.requestAnimationFrame(apply);
      } else {
        apply();
      }
    }

    function setMode(mode) {
      var preview = mode === 'preview';
      var at = scrollFraction();
      btnPreview.classList.toggle('active', preview);
      btnWrite.classList.toggle('active', !preview);
      btnPreview.setAttribute('aria-pressed', String(preview));
      btnWrite.setAttribute('aria-pressed', String(!preview));
      ta.hidden = preview;
      pv.hidden = !preview;
      if (!preview) {
        ta.focus();
        autogrow();
        restoreFraction(at);
        return;
      }
      pv.textContent = 'Loading…';
      var fd = new FormData();
      fd.append('body', ta.value);
      fetch('/admin/posts/preview', { method: 'POST', body: fd, credentials: 'same-origin' })
        .then(function (r) {
          // A redirect here means the session expired: fetch follows it to the
          // login page and hands back a perfectly successful 200 of somebody
          // else's HTML, which would otherwise be injected into the pane as if
          // it were the rendering of this post.
          if (r.redirected || !r.ok) {
            throw new Error('preview failed: ' + r.status);
          }
          return r.text();
        })
        .then(function (html) {
          // The response is this server's own rendering of the body the author
          // just typed, in the author-only admin origin.
          pv.innerHTML = html;
          // Math arrives as raw LaTeX in .math spans; KaTeX has already run
          // once at page load and will not see markup injected afterwards.
          if (typeof window.dtcomTypesetMath === 'function') {
            window.dtcomTypesetMath();
          }
          restoreFraction(at);
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

    // insertImageMarkdown drops ![](url) in and leaves the caret between the
    // brackets, because that empty alt slot is where the caption goes: an image
    // alone in its paragraph renders as a <figure> with its alt text as the
    // visible <figcaption>. Landing the caret after the markdown instead — as
    // this used to — meant every uploaded image shipped without one unless the
    // author went back and clicked between two square brackets.
    function insertImageMarkdown(markdown) {
      var start = ta.selectionStart || 0;
      insertAtCursor(markdown);
      var open = markdown.indexOf('![');
      if (open < 0) {
        return;
      }
      var caret = start + open + 2; // just past the "!["
      if (caret <= ta.value.length) {
        ta.selectionStart = ta.selectionEnd = caret;
      }
    }

    // uploadOne resolves to what the endpoint returned, so a caller that needs
    // two files before it can write anything — the light/dark pair — can wait
    // for both instead of inserting them as they land.
    function uploadOne(file) {
      var fd = new FormData();
      fd.append('file', file, file.name || 'upload');
      return fetch('/admin/images', { method: 'POST', body: fd, credentials: 'same-origin' })
        .then(function (r) {
          return r.json().then(function (body) {
            if (!r.ok) {
              throw new Error(body && body.error ? body.error : 'upload failed');
            }
            return body;
          });
        });
    }

    function inserted() {
      autogrow();
      updateCount();
      stashSoon();
    }

    function upload(file) {
      if (!file || !/^image\//.test(file.type)) {
        return;
      }
      setStatus('Uploading ' + (file.name || 'image') + '…');
      uploadOne(file)
        .then(function (body) {
          insertImageMarkdown(body.markdown || '![](' + body.url + ')');
          inserted();
          setStatus('Inserted ' + body.url + ' — type a caption');
        })
        .catch(function (err) {
          setStatus('Upload failed: ' + err.message, true);
        });
    }

    // --- Light/dark figure ------------------------------------------------
    //
    // A figure that swaps with the theme is two images tagged #light and #dark
    // in one paragraph. It is easy to write by hand and impossible to remember,
    // so the toolbar builds it: the button is the documentation.

    function themeOf(name) {
      // Order matters: "light-to-dark.png" is read as the dark one, which is a
      // coin flip either way and is named back to the author below.
      if (/dark/i.test(name || '')) {
        return 'dark';
      }
      if (/light/i.test(name || '')) {
        return 'light';
      }
      return '';
    }

    // insertPairMarkdown writes the two lines with no blank line between them,
    // because it is the single paragraph that the renderer collapses into one
    // swapping figure — separate them and you get two figures instead.
    //
    // Only the first line gets a caption slot: the renderer takes the caption
    // from the first alt and falls back to the second, so the author types it
    // once and the caret lands where it goes.
    function insertPairMarkdown(lightURL, darkURL) {
      var start = ta.selectionStart || 0;
      insertAtCursor('![](' + lightURL + '#light)\n![](' + darkURL + '#dark)');
      var caret = start + 2; // just past the first "!["
      if (caret <= ta.value.length) {
        ta.selectionStart = ta.selectionEnd = caret;
      }
    }

    function uploadPair(files) {
      var images = Array.prototype.filter.call(files || [], function (f) {
        return f && /^image\//.test(f.type);
      });
      if (images.length !== 2) {
        setStatus('Pick exactly two images: the light one and the dark one.', true);
        return;
      }
      // A file dialog hands back its own ordering, not the order they were
      // clicked, so "the first one is light" would be a guess. The filenames
      // usually say; where they do not, the order stands and the status line
      // names the result, because swapping two tags by hand is only a small fix
      // if you know a guess was made.
      var light = images[0];
      var dark = images[1];
      if (themeOf(light.name) === 'dark' || themeOf(dark.name) === 'light') {
        light = images[1];
        dark = images[0];
      }
      setStatus('Uploading ' + light.name + ' and ' + dark.name + '…');
      Promise.all([uploadOne(light), uploadOne(dark)])
        .then(function (bodies) {
          insertPairMarkdown(bodies[0].url, bodies[1].url);
          inserted();
          setStatus(light.name + ' is the light one, ' + dark.name + ' the dark one — type a caption');
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

    if (btnPair && pairInput) {
      btnPair.addEventListener('click', function () { pairInput.click(); });
      pairInput.addEventListener('change', function () {
        uploadPair(pairInput.files);
        pairInput.value = '';
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
    window.addEventListener('beforeunload', function (e) {
      if (same(snapshot(), saved)) {
        return;
      }
      // Flush past the debounce: the timer will not survive the unload, and
      // this is the moment the draft is most at risk of never coming back.
      writeStash();
      e.preventDefault();
      e.returnValue = '';
    });

    // --- Text editing helpers -------------------------------------------

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

    // The line containing pos, as [start, end) offsets excluding the newline.
    function lineBounds(pos) {
      var start = ta.value.lastIndexOf('\n', pos - 1) + 1;
      var end = ta.value.indexOf('\n', pos);
      return [start, end < 0 ? ta.value.length : end];
    }

    function spaces(n) {
      return new Array(n + 1).join(' ');
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

    // toggleHeading sets, changes or removes the ATX marker on the current
    // line. The same level twice removes it, so the shortcut is one key rather
    // than one key and a way to undo it.
    function toggleHeading(level) {
      var b = lineBounds(ta.selectionStart);
      var caret = ta.selectionStart;
      var m = /^(#{1,6})[ \t]+/.exec(ta.value.slice(b[0], b[1]));
      var want = new Array(level + 1).join('#') + ' ';
      var had = m ? m[0].length : 0;
      var text = m && m[1].length === level ? '' : want;
      replaceRange(b[0], b[0] + had, text);
      ta.selectionStart = ta.selectionEnd = Math.max(b[0], caret + text.length - had);
    }

    // --- Markdown-aware Enter and Tab -------------------------------------

    // LIST matches an item's leading structure: indent, bullet or number, and
    // an optional task box. The indent capture is what makes nesting work — a
    // continued item lands at exactly the depth of the one it follows.
    var LIST = /^([ \t]*)(?:([-*+])|(\d+)([.)]))[ \t]+(\[[ xX]\][ \t]+)?/;
    var QUOTE = /^([ \t]*>[ \t]?)/;

    // nestIndent is how far a child of the item above has to be indented.
    // CommonMark nests a list by the column its parent's text starts in, not by
    // a fixed number of spaces: "- " gives two, "1. " gives three. A flat
    // two-space rule looks right in the textarea and quietly fails to nest
    // under an ordered item.
    function nestIndent(lineStart) {
      var above = ta.value.slice(0, lineStart).split('\n');
      for (var i = above.length - 1; i >= 0; i--) {
        var m = LIST.exec(above[i]);
        if (m) {
          // The task box is part of the item's content, not its marker, so a
          // child of "- [ ] x" nests at two columns like any other bullet.
          return spaces(m[0].length - m[1].length - (m[5] ? m[5].length : 0));
        }
        if (above[i].trim() !== '') {
          break; // a paragraph, not a list: nothing to nest into
        }
      }
      return '  ';
    }

    function shorten(indent, unit) {
      if (indent.charAt(indent.length - 1) === '\t') {
        return indent.slice(0, -1);
      }
      return indent.slice(0, Math.max(0, indent.length - Math.min(unit.length, indent.length)));
    }

    // The marker to open the next item with: the same bullet, the next number,
    // and an unticked box for a task list — carrying the tick over would mark
    // work done before it is written.
    function nextMarker(m) {
      var box = m[5] ? '[ ] ' : '';
      if (m[2]) {
        return m[1] + m[2] + ' ' + box;
      }
      return m[1] + (Number(m[3]) + 1) + m[4] + ' ' + box;
    }

    // continueLine handles Enter inside a list item or blockquote. Returns
    // whether it took the key.
    function continueLine() {
      var caret = ta.selectionStart;
      if (caret !== ta.selectionEnd) {
        return false; // a selection: plain Enter replaces it
      }
      var b = lineBounds(caret);
      var line = ta.value.slice(b[0], b[1]);
      var item = LIST.exec(line);
      var m = item || QUOTE.exec(line);
      if (!m) {
        return false;
      }
      var prefix = m[0];
      var isList = !!item;

      // Enter on an item with nothing in it means "done with this list". One
      // press outdents a nested item to its parent's depth, the next clears the
      // marker outright, so a three-deep list unwinds with three presses rather
      // than leaving the indentation to be deleted by hand.
      if (line.length === prefix.length && caret === b[1]) {
        var indent = isList ? m[1] : '';
        if (indent.length) {
          var shorter = shorten(indent, nestIndent(b[0]));
          replaceRange(b[0], b[0] + indent.length, shorter);
          ta.selectionStart = ta.selectionEnd = b[0] + shorter.length + (prefix.length - indent.length);
        } else {
          replaceRange(b[0], b[1], '');
          ta.selectionStart = ta.selectionEnd = b[0];
        }
        return true;
      }

      var open = '\n' + (isList ? nextMarker(m) : m[1]);
      replaceRange(caret, caret, open);
      ta.selectionStart = ta.selectionEnd = caret + open.length;
      return true;
    }

    // indentLine moves a list item one level in or out. Only list items are
    // taken: everywhere else Tab still moves focus, so the textarea is never a
    // keyboard trap.
    function indentLine(out) {
      if (ta.selectionStart !== ta.selectionEnd) {
        return false; // multi-line selections keep Tab's normal meaning
      }
      var caret = ta.selectionStart;
      var b = lineBounds(caret);
      var m = LIST.exec(ta.value.slice(b[0], b[1]));
      if (!m) {
        return false;
      }
      var unit = nestIndent(b[0]);
      if (out) {
        if (!m[1].length) {
          return false; // already flush left; let Tab move focus
        }
        var shorter = shorten(m[1], unit);
        var dropped = m[1].length - shorter.length;
        replaceRange(b[0], b[0] + m[1].length, shorter);
        ta.selectionStart = ta.selectionEnd = Math.max(b[0], caret - dropped);
        return true;
      }
      replaceRange(b[0], b[0], unit);
      ta.selectionStart = ta.selectionEnd = caret + unit.length;
      return true;
    }

    // --- Key bindings ------------------------------------------------------

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
      if (e.key === 'Enter' && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey) {
        // Shift+Enter is left alone on purpose: it is the way out of a list
        // without ending it.
        if (continueLine()) {
          e.preventDefault();
        }
        return;
      }
      if (e.key === 'Tab' && !e.ctrlKey && !e.metaKey && !e.altKey) {
        if (indentLine(e.shiftKey)) {
          e.preventDefault();
        }
        return;
      }
      if (!(e.ctrlKey || e.metaKey)) {
        return;
      }
      // Headings need Alt as well: Ctrl/⌘+1..6 on its own is the browser's
      // own switch-to-tab-N, which a page cannot intercept. The digit comes
      // from e.code because holding Option on a Mac rewrites e.key to the
      // character it would type.
      if (e.altKey) {
        var digit = /^Digit([1-6])$/.exec(e.code || '');
        if (digit) {
          e.preventDefault();
          toggleHeading(Number(digit[1]));
        }
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

    // Ctrl/Cmd+S saves rather than opening the browser's save dialog. It goes
    // through requestSubmit so the form's own validation still runs.
    document.addEventListener('keydown', function (e) {
      if ((e.ctrlKey || e.metaKey) && e.key === 's' && form) {
        e.preventDefault();
        form.requestSubmit ? form.requestSubmit() : form.submit();
      }
    });

    updateCount();
    autogrow();
    offerRestore();
  });
})();
