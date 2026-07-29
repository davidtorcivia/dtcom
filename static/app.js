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

  // === Lightbox ===
  //
  // Post images are stored at up to 2000px but the reading column is ~1080px,
  // so most of them are being shown smaller than they are. Clicking opens the
  // full-size file.
  //
  // Only images that actually have more to show become clickable. Offering a
  // zoom on a picture already at its natural size is a promise the lightbox
  // cannot keep, and a chart that fits is the common case.
  //
  // Built on <dialog>, like the admin's confirmation prompt: Escape, the
  // backdrop, and returning focus to whatever opened it all come free, and none
  // of it needs an inline script the CSP would refuse.
  function initLightbox() {
    var prose = document.querySelector('.article-prose-body');
    if (!prose) {
      return;
    }
    var box = null;

    function build() {
      var d = document.createElement('dialog');
      d.className = 'lightbox';
      d.innerHTML =
        '<button type="button" class="lightbox-close" aria-label="Close">Close</button>' +
        '<div class="lightbox-stage"><img class="lightbox-img" alt=""></div>' +
        '<p class="lightbox-caption"></p>';
      document.body.appendChild(d);

      d.addEventListener('click', function (e) {
        // A click on the dialog, or on the empty stage around the picture, is
        // the backdrop. Clicks on the image itself are left alone so it can be
        // examined without the thing shutting under the pointer.
        if (e.target === d || e.target.classList.contains('lightbox-close') ||
            e.target.classList.contains('lightbox-stage')) {
          d.close();
        }
      });
      d.addEventListener('close', resetView);
      initGestures(d);
      return d;
    }

    // --- touch gestures ----------------------------------------------------
    //
    // A phone shows the image at a fraction of its size, so "full size" there
    // means nothing without a way to get closer. Pinch to zoom, double-tap to
    // toggle, drag to pan once zoomed, and a downward flick to dismiss.
    //
    // Written against Pointer Events, which covers touch, pen and mouse in one
    // path, and with touch-action: none on the stage so the browser stops
    // trying to scroll and zoom the page underneath us.

    var MAX_SCALE = 6;
    var DISMISS_AT = 90;   // px of downward drag that closes it
    var DOUBLE_TAP_MS = 300;

    var view = { scale: 1, tx: 0, ty: 0 };
    var pointers = null;   // live pointers, by id
    var pinch = null;      // gesture origin while two fingers are down
    var drag = null;       // single-pointer pan or dismiss
    var lastTap = 0;
    var lastTapX = 0;
    var lastTapY = 0;

    function imgEl() { return box && box.querySelector('.lightbox-img'); }
    function stageEl() { return box && box.querySelector('.lightbox-stage'); }

    function applyView(animate) {
      var img = imgEl();
      if (!img) {
        return;
      }
      img.style.transition = animate ? 'transform 0.18s ease-out' : '';
      img.style.transform =
        'translate(' + view.tx + 'px,' + view.ty + 'px) scale(' + view.scale + ')';
      // Once magnified the image itself becomes draggable, so the cursor and
      // the dismiss-on-tap affordance should stop advertising otherwise.
      img.classList.toggle('is-zoomed', view.scale > 1.01);
    }

    function resetView() {
      view.scale = 1;
      view.tx = 0;
      view.ty = 0;
      if (box) {
        var stage = stageEl();
        if (stage) {
          stage.style.opacity = '';
        }
      }
      applyView(false);
    }

    // clampPan keeps the picture's edges from being dragged inside the frame:
    // there is nothing behind them, and letting go of the image is disorienting.
    function clampPan() {
      var img = imgEl(), stage = stageEl();
      if (!img || !stage) {
        return;
      }
      var maxX = Math.max(0, (img.offsetWidth * view.scale - stage.clientWidth) / 2);
      var maxY = Math.max(0, (img.offsetHeight * view.scale - stage.clientHeight) / 2);
      view.tx = Math.min(maxX, Math.max(-maxX, view.tx));
      view.ty = Math.min(maxY, Math.max(-maxY, view.ty));
    }

    // zoomAbout scales while holding one point still, so the picture grows
    // around the fingers rather than around its own middle.
    function zoomAbout(nextScale, px, py) {
      var stage = stageEl();
      if (!stage) {
        return;
      }
      var r = stage.getBoundingClientRect();
      var ax = px - r.left - r.width / 2;
      var ay = py - r.top - r.height / 2;
      var ratio = nextScale / view.scale;
      view.tx = ax - (ax - view.tx) * ratio;
      view.ty = ay - (ay - view.ty) * ratio;
      view.scale = nextScale;
      clampPan();
    }

    function distance(a, b) {
      return Math.hypot(a.x - b.x, a.y - b.y);
    }

    function pointerList() {
      var out = [];
      pointers.forEach(function (p) { out.push(p); });
      return out;
    }

    function initGestures(d) {
      pointers = {};
      pointers.map = Object.create(null);
      pointers.forEach = function (fn) {
        Object.keys(this.map).forEach(function (k) { fn(this.map[k]); }, this);
      };
      pointers.set = function (id, p) { this.map[id] = p; };
      pointers.del = function (id) { delete this.map[id]; };
      pointers.count = function () { return Object.keys(this.map).length; };

      var stage = d.querySelector('.lightbox-stage');

      stage.addEventListener('pointerdown', function (e) {
        pointers.set(e.pointerId, { id: e.pointerId, x: e.clientX, y: e.clientY, type: e.pointerType });
        if (stage.setPointerCapture) {
          try { stage.setPointerCapture(e.pointerId); } catch (err) { /* not capturable */ }
        }
        var n = pointers.count();
        if (n === 2) {
          var ps = pointerList();
          pinch = {
            dist: distance(ps[0], ps[1]),
            scale: view.scale,
            midX: (ps[0].x + ps[1].x) / 2,
            midY: (ps[0].y + ps[1].y) / 2,
          };
          drag = null;
        } else if (n === 1) {
          drag = { x: e.clientX, y: e.clientY, tx: view.tx, ty: view.ty, type: e.pointerType, moved: 0 };
        }
      });

      stage.addEventListener('pointermove', function (e) {
        var p = pointers.map[e.pointerId];
        if (!p) {
          return;
        }
        p.x = e.clientX;
        p.y = e.clientY;

        if (pinch && pointers.count() === 2) {
          var ps = pointerList();
          var next = Math.min(MAX_SCALE, Math.max(1, pinch.scale * (distance(ps[0], ps[1]) / pinch.dist)));
          zoomAbout(next, (ps[0].x + ps[1].x) / 2, (ps[0].y + ps[1].y) / 2);
          applyView(false);
          return;
        }
        if (!drag) {
          return;
        }
        var dx = e.clientX - drag.x;
        var dy = e.clientY - drag.y;
        drag.moved = Math.max(drag.moved, Math.hypot(dx, dy));

        if (view.scale > 1.01) {
          view.tx = drag.tx + dx;
          view.ty = drag.ty + dy;
          clampPan();
          applyView(false);
          return;
        }
        // Not zoomed: a downward drag on touch dismisses, the way a photo
        // viewer does. The image follows the finger and the stage fades, so the
        // gesture shows its own progress before it commits to anything.
        if (drag.type === 'touch' && dy > 0) {
          view.ty = dy;
          stage.style.opacity = String(Math.max(0.25, 1 - dy / 400));
          applyView(false);
        }
      });

      function endPointer(e) {
        var was = pointers.map[e.pointerId];
        pointers.del(e.pointerId);

        if (pointers.count() < 2) {
          pinch = null;
        }
        if (pointers.count() > 0) {
          return;
        }

        // Dismiss if the flick went far enough; otherwise spring back.
        if (drag && drag.type === 'touch' && view.scale <= 1.01) {
          if (view.ty > DISMISS_AT) {
            drag = null;
            box.close();
            return;
          }
          view.ty = 0;
          stage.style.opacity = '';
          applyView(true);
        }
        // A pinch that ended below 1x snaps back rather than leaving the
        // picture stranded small in the middle of a black screen.
        if (view.scale <= 1.01 && view.scale !== 1) {
          resetView();
          applyView(true);
        }

        // Double-tap toggles between fit and a close-up on the tapped point.
        // Only when the finger stayed put — a pan should never zoom.
        if (was && drag && drag.moved < 10) {
          var now = Date.now();
          var near = Math.hypot(was.x - lastTapX, was.y - lastTapY) < 40;
          if (now - lastTap < DOUBLE_TAP_MS && near) {
            if (view.scale > 1.01) {
              resetView();
            } else {
              zoomAbout(2.5, was.x, was.y);
            }
            applyView(true);
            lastTap = 0;
          } else {
            lastTap = now;
            lastTapX = was.x;
            lastTapY = was.y;
          }
        }
        drag = null;
      }

      stage.addEventListener('pointerup', endPointer);
      stage.addEventListener('pointercancel', endPointer);

      // Desktop: the wheel zooms about the cursor, which is what anyone who has
      // used a map expects. Passive:false because zooming means not scrolling.
      stage.addEventListener('wheel', function (e) {
        if (!box || !box.open) {
          return;
        }
        e.preventDefault();
        var next = Math.min(MAX_SCALE, Math.max(1, view.scale * (e.deltaY < 0 ? 1.12 : 1 / 1.12)));
        zoomAbout(next, e.clientX, e.clientY);
        if (next === 1) {
          view.tx = 0;
          view.ty = 0;
        }
        applyView(false);
      }, { passive: false });
    }

    // zoomable reports whether the file has detail the page is not showing.
    // A hidden image — the unused half of a light/dark pair — has no layout
    // width at all, so it is skipped rather than counted as infinitely zoomable.
    function zoomable(img) {
      if (!img.naturalWidth || !img.clientWidth) {
        return false;
      }
      // A couple of pixels of slack: layout rounding should not make an image
      // that exactly fits look like it has more to give.
      return img.naturalWidth > img.clientWidth + 2;
    }

    function captionFor(img) {
      var fig = img.closest ? img.closest('figure') : null;
      var cap = fig ? fig.querySelector('figcaption') : null;
      if (cap && cap.textContent.trim()) {
        return cap.textContent.trim();
      }
      return img.getAttribute('alt') || '';
    }

    function open(img) {
      if (!box) {
        box = build();
      }
      if (typeof box.showModal !== 'function') {
        return; // no <dialog> support: the image is still there on the page
      }
      var full = box.querySelector('.lightbox-img');
      full.src = img.currentSrc || img.src;
      full.alt = img.getAttribute('alt') || '';
      var caption = box.querySelector('.lightbox-caption');
      caption.textContent = captionFor(img);
      caption.hidden = !caption.textContent;
      // Always open at fit, never at whatever magnification the last picture
      // was left on.
      resetView();
      box.showModal();
      box.querySelector('.lightbox-close').focus();
    }

    function mark(img) {
      var can = zoomable(img);
      img.classList.toggle('is-zoomable', can);
      if (can) {
        // An <img> is not focusable, so keyboard users need both of these to
        // reach it at all.
        img.setAttribute('tabindex', '0');
        img.setAttribute('role', 'button');
        img.setAttribute('aria-label', 'View full size: ' + (captionFor(img) || 'image'));
      } else {
        img.removeAttribute('tabindex');
        img.removeAttribute('role');
        img.removeAttribute('aria-label');
      }
    }

    var imgs = Array.prototype.slice.call(prose.querySelectorAll('img'));
    if (!imgs.length) {
      return;
    }

    function markAll() {
      imgs.forEach(mark);
    }

    imgs.forEach(function (img) {
      if (img.complete) {
        mark(img);
      } else {
        img.addEventListener('load', function () { mark(img); });
      }
      img.addEventListener('click', function () {
        if (zoomable(img)) {
          open(img);
        }
      });
      img.addEventListener('keydown', function (e) {
        if ((e.key === 'Enter' || e.key === ' ') && zoomable(img)) {
          e.preventDefault();
          open(img);
        }
      });
    });

    // Whether an image has room to grow depends on the column width, so the
    // answer changes when the window does.
    var resizeTimer = null;
    window.addEventListener('resize', function () {
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(markAll, 150);
    });

    // Switching theme swaps which half of a light/dark pair is displayed, and
    // the newly visible one has only just acquired a layout width.
    if (window.MutationObserver) {
      new MutationObserver(markAll).observe(document.documentElement, {
        attributes: true,
        attributeFilter: ['data-theme'],
      });
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
    initLightbox();
  });
})();
