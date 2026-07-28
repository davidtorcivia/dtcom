// Typesets the math the build engine left in the page.
//
// Markdown rendering does not convert LaTeX — it protects it. Each formula
// arrives as <span class="math inline">\(…\)</span> or
// <span class="math display">\[…\]</span> with the source verbatim inside,
// because ordinary markdown would otherwise collapse `\\` row separators to a
// single backslash and turn subscript underscores into <em>.
//
// This walks those spans and hands each one to KaTeX. Deliberately not
// KaTeX's auto-render extension: that scans the whole document for delimiters
// and would find any stray `$` in prose. The spans are unambiguous, already
// marked display or inline, and cost nothing to enumerate.
(function () {
  'use strict';

  function typeset() {
    if (typeof katex === 'undefined') {
      // The stylesheet leaves .math readable as monospace source, so a failed
      // or blocked script degrades to visible LaTeX rather than a blank gap.
      return;
    }
    var nodes = document.querySelectorAll('.math');
    for (var i = 0; i < nodes.length; i++) {
      var el = nodes[i];
      if (el.classList.contains('is-typeset')) {
        continue;
      }
      var display = el.classList.contains('display');
      var tex = el.textContent.trim();
      // Strip the delimiters the markdown extension wrapped around the source.
      if (display && tex.indexOf('\\[') === 0 && tex.lastIndexOf('\\]') === tex.length - 2) {
        tex = tex.slice(2, -2);
      } else if (!display && tex.indexOf('\\(') === 0 && tex.lastIndexOf('\\)') === tex.length - 2) {
        tex = tex.slice(2, -2);
      }
      try {
        katex.render(tex, el, {
          displayMode: display,
          // A typo in one formula must not blank the rest of the page: KaTeX
          // renders the offending source in red and carries on.
          throwOnError: false,
          strict: false,
        });
        el.classList.add('is-typeset');
      } catch (e) {
        // Leave the source visible; it is more use than an empty box.
      }
    }
  }

  // Exposed so the admin editor can re-typeset after rendering a preview,
  // which injects fresh .math spans long after DOMContentLoaded.
  window.dtcomTypesetMath = typeset;

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', typeset);
  } else {
    typeset();
  }
})();
