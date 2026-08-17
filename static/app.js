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
    // The referring site travels with the view. The server keeps the hostname
    // and throws the rest away.
    beacon({ path: path, ref: document.referrer || '' });

    // Reading time, counted only while the tab is actually visible and
    // reported when it stops being — 'hidden' is the last moment a phone
    // browser reliably runs script, and 'unload' has not been dependable for
    // years. Time spent after a report is sent on the next one; the server
    // adds them up.
    var since = Date.now();
    var spent = 0;
    document.addEventListener('visibilitychange', function () {
      if (document.visibilityState === 'hidden') {
        spent += Math.round((Date.now() - since) / 1000);
        if (spent > 0) {
          beacon({ path: path, dwell: spent });
          spent = 0;
        }
      }
      since = Date.now();
    });
  }

  function beacon(payload) {
    try {
      navigator.sendBeacon('/api/track', new Blob([JSON.stringify(payload)], { type: 'application/json' }));
    } catch (e) {
      // A blocked beacon must never break the page.
    }
  }

  // === Lightbox ===
  //
  // What a page shows is a rendition: the server cuts every stored picture down
  // to a set of widths and the browser takes the one that fits the column, so
  // on a phone the visible copy can be a fifth of the real thing. Clicking
  // opens the master.
  //
  // That makes the master's dimensions, not the visible copy's, the number
  // every decision here turns on — how far a zoom can go, whether there is
  // anything to zoom into at all. They arrive in the markup as data-full-w, so
  // they are known before a byte of the master has been fetched.
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
      // No caption in here on purpose: the caption is on the page, under the
      // figure, and it has already been read by the time anyone opens this. The
      // lightbox is for looking at the picture.
      d.innerHTML =
        '<button type="button" class="lightbox-close" aria-label="Close">Close</button>' +
        '<p class="lightbox-loading" role="status" aria-live="polite" hidden>Loading full size</p>' +
        '<div class="lightbox-stage"><img class="lightbox-img" alt=""></div>';
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
      // A fallback for pictures the server could not measure for us — a remote
      // image, or the admin preview, where there is no data-full-w to read. The
      // guard on base matters: a load also fires when the full-size file
      // arrives and replaces what is on screen, and re-measuring there would
      // throw away the zoom the reader is in the middle of.
      d.querySelector('.lightbox-img').addEventListener('load', function () {
        if (d.open && !base && measure()) {
          applyView(false);
        }
      });

      d.addEventListener('close', function () {
        // Drop the measured geometry along with the view. The next picture has
        // a size of its own, and inheriting this one's would show it at the
        // wrong size for a frame.
        resetView();
        var img = imgEl();
        if (img) {
          img.classList.remove('is-measured', 'is-gesturing', 'is-zoomed');
          img.style.width = '';
          img.style.height = '';
        }
        base = null;
        layoutScale = 1;
        // Invalidate any full-size file still in flight, so it cannot arrive
        // and paint itself over whatever is opened next.
        openToken++;
        showLoading(false);
      });
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

    var MIN_MAX_SCALE = 6;   // never offer less magnification than this
    var ABS_MAX_SCALE = 20;  // ...and never more, whatever the file's size
    var DISMISS_AT = 90;     // px of downward drag that closes it
    var DOUBLE_TAP_MS = 300;
    var COMMIT_MS = 90;      // quiet time before the zoom is re-rasterised

    // view.scale is the zoom relative to the fit size, and it is the only
    // measure of magnification the gestures deal in.
    var view = { scale: 1, tx: 0, ty: 0 };
    // base is the fit geometry in CSS pixels, plus the file's own dimensions.
    var base = null;
    // master describes the full-size file behind the picture on the page: what
    // the page shows is a rendition cut down to the width of the column, and
    // zooming into that would be zooming into a thumbnail. Its dimensions are
    // known from the markup before a byte of it has been fetched, which is what
    // lets the zoom be scaled to the real thing from the moment it opens.
    var master = null;
    // Bumped on every open and close, so a full-size file that arrives late
    // can tell it is no longer wanted.
    var openToken = 0;
    // layoutScale is how much of view.scale has been baked into the element's
    // width and height; the rest is left to the transform. See commitRaster.
    var layoutScale = 1;
    var commitTimer = null;
    var pointers = null;   // live pointers, by id
    var pinch = null;      // gesture origin while two fingers are down
    var drag = null;       // single-pointer pan or dismiss
    var lastTap = 0;
    var lastTapX = 0;
    var lastTapY = 0;

    function imgEl() { return box && box.querySelector('.lightbox-img'); }
    function stageEl() { return box && box.querySelector('.lightbox-stage'); }

    // measure sizes the picture to fit the stage and remembers that geometry.
    //
    // The layout size used to be left to CSS (max-width: 100%), which meant a
    // phone laid a 2000px file out at ~390px and the browser rasterised — and
    // often decoded — it at that size. Everything a pinch did after that was
    // stretching those 390px. Owning the size here is what lets commitRaster
    // ask for a bigger one.
    function measure() {
      var img = imgEl(), stage = stageEl();
      if (!img || !stage) {
        return false;
      }
      // The full-size file's dimensions, not the rendition's. They are the same
      // shape, so the fit is unaffected, but everything about how far the zoom
      // can go depends on which one it is — and the rendition on screen may be
      // a fifth of the real width.
      var natW = (master && master.w) || img.naturalWidth;
      var natH = (master && master.h) || img.naturalHeight;
      if (!natW || !natH) {
        return false;
      }
      var cs = window.getComputedStyle(stage);
      var availW = stage.clientWidth -
        (parseFloat(cs.paddingLeft) || 0) - (parseFloat(cs.paddingRight) || 0);
      var availH = stage.clientHeight -
        (parseFloat(cs.paddingTop) || 0) - (parseFloat(cs.paddingBottom) || 0);
      if (availW <= 0 || availH <= 0) {
        return false;
      }
      // Fit, and never upscale past the file's own resolution: the same rule
      // the stylesheet used to apply, now with the numbers in hand.
      var fit = Math.min(availW / natW, availH / natH, 1);
      base = {
        w: natW * fit,
        h: natH * fit,
        natW: natW,
        natH: natH,
        // The frame the picture is centred in is the stage's content box, not
        // its padding box, and the padding is not symmetrical — the close
        // button gets a strip at one end. Panning is bounded against the same
        // box the fit was measured against, so the edges line up.
        availW: availW,
        availH: availH,
        padL: parseFloat(cs.paddingLeft) || 0,
        padT: parseFloat(cs.paddingTop) || 0,
      };
      layoutScale = 1;
      img.classList.add('is-measured');
      img.style.width = base.w + 'px';
      img.style.height = base.h + 'px';
      return true;
    }

    // maxScale is the ceiling on magnification. Six times fit is plenty on a
    // desktop, where the picture already opens near its full size, but on a
    // phone the fit view shows a 2000px file at about a fifth of itself and
    // stopping at 6x would stop short of detail the file actually holds. So
    // the real limit is "far enough to put every stored pixel on the screen,
    // with half again for a close look", and 6x is only the floor.
    function maxScale() {
      if (!base || !base.w) {
        return MIN_MAX_SCALE;
      }
      return Math.min(ABS_MAX_SCALE,
        Math.max(MIN_MAX_SCALE, (base.natW / base.w) * 1.5));
    }

    // commitRaster bakes the current zoom into the element's layout size.
    //
    // This is the fix for a blurry zoom. A transform scale is composited: the
    // browser rasterises the layer once, at the size the layout asked for, and
    // magnifying it enlarges those pixels rather than drawing new ones — which
    // on a phone means zooming into a picture rendered at phone width. Setting
    // the width and height instead is a layout change, and a layout change is
    // always redrawn from the decoded image at the new size.
    //
    // The transform still runs the gesture, because it is smooth and layout is
    // not; this only lands once the fingers are off. The two are swapped in a
    // single frame with the transition off, so the picture sharpens in place
    // rather than moving.
    // nativeScale is the zoom at which every stored pixel lands on a device
    // pixel: the sharpest the file can ever be shown, and the last point worth
    // redrawing at. Past it there is nothing left to draw, and the layer only
    // gets more expensive to hold — which on iOS is how a zoomed image turns
    // into a blank rectangle.
    function nativeScale() {
      if (!base || !base.w) {
        return 1;
      }
      return Math.max(1, base.natW / ((window.devicePixelRatio || 1) * base.w));
    }

    function commitRaster() {
      var img = imgEl();
      if (!img || !base || !box || !box.open) {
        return;
      }
      // Beyond nativeScale the transform can go on stretching; there is no
      // detail being withheld.
      var target = Math.min(view.scale, nativeScale());
      if (Math.abs(target - layoutScale) < 0.02) {
        return;
      }
      layoutScale = target;
      img.style.width = (base.w * layoutScale) + 'px';
      img.style.height = (base.h * layoutScale) + 'px';
      applyView(false);
    }

    function scheduleCommit(delay) {
      clearTimeout(commitTimer);
      commitTimer = setTimeout(commitRaster, delay === undefined ? COMMIT_MS : delay);
    }

    // --- the full-size file -------------------------------------------------
    //
    // The lightbox opens on whatever the page already has: that file is decoded
    // and in memory, so the picture is on screen in the same frame as the
    // dialog. It is also a rendition — as narrow as 480px on a phone — so the
    // full-size master is fetched straight afterwards and swapped in when it is
    // ready, by which time the reader has usually not started zooming.
    //
    // Fetched through a detached Image so the swap happens from cache, with no
    // moment where the element has a src it has not decoded and paints white.

    function showLoading(on) {
      if (!box) {
        return;
      }
      var el = box.querySelector('.lightbox-loading');
      if (el) {
        el.hidden = !on;
      }
    }

    // fetchMaster loads one candidate and, if the browser cannot decode it,
    // falls back to the next. The WebP master is offered first where there is
    // one — it is the same pixels in fewer bytes — and a browser too old to
    // read WebP simply fails to decode it and gets the PNG.
    //
    // That failure is the whole feature detection. Probing for WebP support
    // costs a request or a canvas trick that gives the wrong answer on the
    // Safari versions that read the format but cannot write it; letting the
    // load itself answer is both cheaper and exactly right.
    function fetchMaster(candidates, token) {
      if (!candidates.length) {
        showLoading(false);
        return;
      }
      var url = candidates[0];
      var pre = new Image();
      // The picture is already readable at rendition quality, so this must not
      // compete with anything the page is still doing.
      pre.fetchPriority = 'low';
      pre.decoding = 'async';
      pre.onload = function () {
        // Gone, or superseded by another picture, while this was in flight.
        if (token !== openToken || !box || !box.open) {
          return;
        }
        var img = imgEl();
        if (img) {
          // No geometry is touched: the element keeps the explicit size the
          // zoom gave it, and only the pixels behind it get better. Which is
          // the whole trick — the swap is invisible except for the sharpening.
          img.src = url;
        }
        showLoading(false);
      };
      pre.onerror = function () {
        if (token === openToken) {
          fetchMaster(candidates.slice(1), token);
        }
      };
      pre.src = url;
    }

    function loadMaster() {
      if (!master || !master.url) {
        return;
      }
      var candidates = [];
      if (master.webp) {
        candidates.push(master.webp);
      }
      candidates.push(master.url);
      showLoading(true);
      fetchMaster(candidates, openToken);
    }

    function applyView(animate) {
      var img = imgEl();
      if (!img) {
        return;
      }
      img.style.transition = animate ? 'transform 0.18s ease-out' : '';
      // Only the part of the zoom the layout is not already carrying goes into
      // the transform. Both are relative to the fit size, so their product is
      // always view.scale and the picture does not move when one becomes the
      // other.
      var residual = view.scale / (layoutScale || 1);
      img.style.transform =
        'translate(' + view.tx + 'px,' + view.ty + 'px) scale(' + residual + ')';
      // Once magnified the image itself becomes draggable, so the cursor and
      // the dismiss-on-tap affordance should stop advertising otherwise.
      img.classList.toggle('is-zoomed', view.scale > 1.01);
    }

    function resetView() {
      view.scale = 1;
      view.tx = 0;
      view.ty = 0;
      clearTimeout(commitTimer);
      if (box) {
        var stage = stageEl();
        if (stage) {
          stage.style.opacity = '';
        }
        var img = imgEl();
        if (img && base) {
          layoutScale = 1;
          img.style.width = base.w + 'px';
          img.style.height = base.h + 'px';
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
      // The displayed size is the fit size times the zoom however it is
      // currently split between layout and transform, so it is read from the
      // measurement rather than from the element.
      var shownW = base ? base.w * view.scale : img.offsetWidth * view.scale;
      var shownH = base ? base.h * view.scale : img.offsetHeight * view.scale;
      var frameW = base ? base.availW : stage.clientWidth;
      var frameH = base ? base.availH : stage.clientHeight;
      var maxX = Math.max(0, (shownW - frameW) / 2);
      var maxY = Math.max(0, (shownH - frameH) / 2);
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
      // Measured from where the picture actually sits, which is the middle of
      // the stage's content box — the padding is one-sided, since the close
      // button needs a strip clear at one end.
      var cx = base ? r.left + base.padL + base.availW / 2 : r.left + r.width / 2;
      var cy = base ? r.top + base.padT + base.availH / 2 : r.top + r.height / 2;
      var ax = px - cx;
      var ay = py - cy;
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
        // A commit mid-gesture would resize the element under the fingers, so
        // any pending one waits for them to leave.
        clearTimeout(commitTimer);
        var img = imgEl();
        if (img) {
          img.classList.add('is-gesturing');
        }
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
          var next = Math.min(maxScale(), Math.max(1, pinch.scale * (distance(ps[0], ps[1]) / pinch.dist)));
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
        // Every finger is off. The will-change hint goes with them: it pins the
        // layer's raster scale, which is exactly what has to be let go of
        // before the picture can be redrawn sharp at the zoom it landed on.
        var img = imgEl();
        if (img) {
          img.classList.remove('is-gesturing');
        }
        var eased = false;

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
          eased = true;
        }
        // A pinch that ended below 1x snaps back rather than leaving the
        // picture stranded small in the middle of a black screen.
        if (view.scale <= 1.01 && view.scale !== 1) {
          resetView();
          applyView(true);
          eased = true;
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
              // Land on the sharpest view the file has in it rather than on a
              // round number: on a phone that is every one of a 2000px file's
              // pixels, and the difference between reading a label and
              // guessing at it. Where that is barely a magnification — a
              // picture already near its own resolution on screen — a flat
              // 2.5x is the more useful answer, and no less sharp, since there
              // is nothing to land on.
              var native = nativeScale();
              zoomAbout(native >= 1.5 ? native : 2.5, was.x, was.y);
            }
            applyView(true);
            eased = true;
            lastTap = 0;
          } else {
            lastTap = now;
            lastTapX = was.x;
            lastTapY = was.y;
          }
        }
        drag = null;
        // Redraw at the zoom the gesture settled on. Later when something is
        // still easing, so the swap does not land in the middle of it.
        scheduleCommit(eased ? 260 : COMMIT_MS);
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
        var next = Math.min(maxScale(), Math.max(1, view.scale * (e.deltaY < 0 ? 1.12 : 1 / 1.12)));
        zoomAbout(next, e.clientX, e.clientY);
        if (next === 1) {
          view.tx = 0;
          view.ty = 0;
        }
        applyView(false);
        // A wheel arrives as a burst of events; the redraw waits for the end
        // of it rather than resizing the element on every notch.
        scheduleCommit(180);
      }, { passive: false });
    }

    // masterOf reads the full-size file the server recorded beside the page's
    // rendition. Absent for a remote image or an SVG, in which case what is on
    // the page is all there is.
    //
    // The WebP master is preferred when the page itself is showing WebP, which
    // is proof enough that this browser reads the format — no probing, no
    // canvas trick, just the evidence already on screen.
    function masterOf(img) {
      var url = img.getAttribute('data-full');
      if (!url) {
        return null;
      }
      return {
        url: url,
        webp: img.getAttribute('data-full-webp') || '',
        w: parseInt(img.getAttribute('data-full-w'), 10) || 0,
        h: parseInt(img.getAttribute('data-full-h'), 10) || 0,
      };
    }

    // zoomable reports whether the file has detail the page is not showing.
    // A hidden image — the unused half of a light/dark pair — has no layout
    // width at all, so it is skipped rather than counted as infinitely zoomable.
    function zoomable(img) {
      if (!img.clientWidth) {
        return false;
      }
      // The comparison is against the full-size file, not the rendition on the
      // page. A phone is sent a 480px-wide copy of a 2560px picture: measured
      // by what it has decoded, that copy fits the column exactly and looks
      // like it has nothing more to show, which is the opposite of the truth.
      var m = masterOf(img);
      var width = (m && m.w) || img.naturalWidth;
      if (!width) {
        return false;
      }
      // A couple of pixels of slack: layout rounding should not make an image
      // that exactly fits look like it has more to give.
      return width > img.clientWidth + 2;
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
      // Start from no geometry at all: whatever the last picture was measured
      // and zoomed to says nothing about this one.
      base = null;
      layoutScale = 1;
      openToken++;
      full.classList.remove('is-measured', 'is-zoomed', 'is-gesturing');
      full.style.width = '';
      full.style.height = '';
      master = masterOf(img);
      // Opens on the rendition the page already has. It is decoded and in
      // memory, so the picture is there in the frame the dialog appears in,
      // rather than after a request. loadMaster replaces it below.
      var shown = img.currentSrc || img.src;
      full.src = shown;
      // The alt still travels, even though nothing is drawn from it: the
      // picture in the dialog needs its own description for a screen reader.
      full.alt = img.getAttribute('alt') || '';
      // Always open at fit, never at whatever magnification the last picture
      // was left on.
      resetView();
      box.showModal();
      // Measured after showModal, since a closed dialog's stage has no size to
      // fit against. With a master recorded this needs nothing to have loaded —
      // its dimensions came from the markup. Without one, the load handler in
      // build() measures whenever the file finishes decoding.
      if (measure()) {
        applyView(false);
      }
      // Nothing to fetch when the page was already showing the full-size file,
      // which is the case for a picture small enough to have no renditions.
      if (master && master.url && shown.indexOf(master.url) < 0 &&
          (!master.webp || shown.indexOf(master.webp) < 0)) {
        loadMaster();
      }
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
      // Marked straight away, not on load. Most pictures on a page are below
      // the fold and load lazily, and their full size is known from the markup
      // long before any of their bytes arrive — waiting would leave them
      // looking un-openable until the reader happened to scroll past them.
      mark(img);
      if (!img.complete) {
        // Still worth repeating on load, for the images with no recorded size,
        // where the answer genuinely is not known until then.
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
      resizeTimer = setTimeout(function () {
        markAll();
        // An open lightbox is fitted to the stage, which has just changed size
        // — a phone turned sideways would otherwise keep its portrait
        // geometry, and the zoom maths with it.
        if (box && box.open && measure()) {
          resetView();
        }
      }, 150);
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
