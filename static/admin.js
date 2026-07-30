// Admin page interactions that need scripting: confirming destructive actions,
// revealing the API token, and copying values to the clipboard.
//
// A separate file rather than inline handlers, because the Content-Security-
// Policy forbids inline script — see the middleware in internal/server. That
// also rules out `onsubmit="return confirm(...)"`, which is an inline handler
// and is blocked outright: the confirmations below replace it, and are styled
// to match the rest of the admin rather than using the browser's dialog.
(function () {
  'use strict';

  // --- Confirmation dialog ------------------------------------------------

  var dialog = null;
  var pendingForm = null;

  function buildDialog() {
    var d = document.createElement('dialog');
    d.className = 'confirm-dialog';
    d.innerHTML =
      '<form method="dialog" class="confirm-body">' +
      '<h2 class="confirm-title"></h2>' +
      '<p class="confirm-detail"></p>' +
      '<div class="confirm-actions">' +
      '<button value="cancel" class="confirm-cancel" type="submit">Cancel</button>' +
      '<button value="confirm" class="confirm-go" type="submit"></button>' +
      '</div>' +
      '</form>';
    document.body.appendChild(d);

    d.addEventListener('close', function () {
      var form = pendingForm;
      pendingForm = null;
      if (d.returnValue === 'confirm' && form) {
        // Mark it so the submit handler lets this one through.
        form.dataset.confirmed = 'yes';
        if (form.requestSubmit) {
          form.requestSubmit();
        } else {
          form.submit();
        }
      }
    });
    return d;
  }

  function ask(form) {
    if (!dialog) {
      dialog = buildDialog();
    }
    dialog.querySelector('.confirm-title').textContent = form.dataset.confirm || 'Are you sure?';
    var detail = dialog.querySelector('.confirm-detail');
    detail.textContent = form.dataset.confirmDetail || '';
    detail.hidden = !form.dataset.confirmDetail;
    dialog.querySelector('.confirm-go').textContent = form.dataset.confirmAction || 'Delete';
    pendingForm = form;
    dialog.returnValue = '';

    if (typeof dialog.showModal === 'function') {
      dialog.showModal();
      dialog.querySelector('.confirm-cancel').focus();
      return;
    }
    // <dialog> is very widely supported; this is the last-resort path so a
    // destructive action is never silently unguarded.
    pendingForm = null;
    if (window.confirm(form.dataset.confirm || 'Are you sure?')) {
      form.dataset.confirmed = 'yes';
      form.submit();
    }
  }

  // Capture phase, so the check runs before any other submit handler.
  document.addEventListener('submit', function (e) {
    var form = e.target;
    if (!form.dataset || !form.dataset.confirm) {
      return;
    }
    if (form.dataset.confirmed === 'yes') {
      delete form.dataset.confirmed; // let it proceed, once
      return;
    }
    e.preventDefault();
    ask(form);
  }, true);

  // --- Copy / reveal ------------------------------------------------------

  function flash(button, text) {
    var original = button.dataset.originalLabel || button.textContent;
    button.dataset.originalLabel = original;
    button.textContent = text;
    button.classList.add('is-done');
    setTimeout(function () {
      button.textContent = original;
      button.classList.remove('is-done');
    }, 1400);
  }

  // Clipboard access needs a secure context (https or localhost); the
  // textarea fallback covers a plain-http deployment behind a proxy.
  function copyText(text) {
    if (navigator.clipboard && window.isSecureContext) {
      return navigator.clipboard.writeText(text);
    }
    return new Promise(function (resolve, reject) {
      var ta = document.createElement('textarea');
      ta.value = text;
      ta.setAttribute('readonly', '');
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      var ok = false;
      try {
        ok = document.execCommand('copy');
      } catch (e) {
        ok = false;
      }
      document.body.removeChild(ta);
      ok ? resolve() : reject(new Error('copy failed'));
    });
  }

  document.addEventListener('click', function (e) {
    var button = e.target.closest('[data-action]');
    if (!button) {
      return;
    }

    if (button.dataset.action === 'copy') {
      e.preventDefault();
      copyText(button.dataset.copy || '')
        .then(function () { flash(button, 'Copied'); })
        .catch(function () { flash(button, 'Press ⌘/Ctrl+C'); });
      return;
    }

    if (button.dataset.action === 'reveal') {
      e.preventDefault();
      var target = document.getElementById(button.dataset.target);
      if (!target) {
        return;
      }
      var hidden = target.textContent === target.dataset.mask;
      target.textContent = hidden ? target.dataset.token : target.dataset.mask;
      button.textContent = hidden ? 'Hide' : 'Reveal';
      button.dataset.originalLabel = button.textContent;
    }
  });

  // --- Chart tooltip ------------------------------------------------------
  //
  // The dashboard's bars carry their day and count in data-* attributes and a
  // title, so pointing at one says something with scripting off too. This
  // upgrades that to a readout that follows the cursor and does not wait for
  // the browser's tooltip delay.
  //
  // One element, reused: a chart can be 90 columns wide and building a node
  // per bar would be 90 nodes to keep in sync with nothing.

  var tip = null;
  var tipCol = null;

  function chartTip() {
    if (!tip) {
      tip = document.createElement('div');
      tip.className = 'chart-tip';
      tip.setAttribute('role', 'status');
      tip.innerHTML = '<span class="chart-tip-count"></span> <span class="chart-tip-label"></span>';
      document.body.appendChild(tip);
    }
    return tip;
  }

  function showTip(col) {
    var t = chartTip();
    var count = col.dataset.count || '0';
    t.querySelector('.chart-tip-count').textContent = count === '1' ? '1 view' : count + ' views';
    t.querySelector('.chart-tip-label').textContent = col.dataset.label || '';

    // Anchored to the top of the bar rather than to the cursor, so the readout
    // holds still while the pointer moves and sits where the value actually
    // is — at the baseline for a day nobody visited, high up for a busy one.
    var bar = col.querySelector('.chart-bar') || col;
    var box = bar.getBoundingClientRect();
    var x = box.left + box.width / 2;
    // Keep it on screen: a tooltip over the first or last bar of a wide chart
    // would otherwise hang off the edge.
    var half = t.offsetWidth / 2;
    if (half) {
      x = Math.min(Math.max(x, half + 4), window.innerWidth - half - 4);
    }
    t.style.left = x + 'px';
    t.style.top = (box.top - 6) + 'px';
    t.classList.add('is-visible');

    if (tipCol && tipCol !== col) {
      tipCol.classList.remove('is-hot');
    }
    col.classList.add('is-hot');
    tipCol = col;
  }

  function hideTip() {
    if (tip) {
      tip.classList.remove('is-visible');
    }
    if (tipCol) {
      tipCol.classList.remove('is-hot');
      tipCol = null;
    }
  }

  document.addEventListener('pointermove', function (e) {
    var col = e.target.closest ? e.target.closest('.chart-col') : null;
    if (!col) {
      if (tipCol) {
        hideTip();
      }
      return;
    }
    if (col !== tipCol) {
      showTip(col);
    }
  });

  // Scrolling moves the bars out from under a tooltip pinned to the viewport.
  window.addEventListener('scroll', hideTip, { passive: true });
  window.addEventListener('blur', hideTip);

  // Keyboard and touch: focus or tap a column to read it. The columns are
  // given tabindex here rather than in the template so that a browser without
  // scripting does not offer a tab stop that does nothing.
  var charts = document.querySelectorAll('[data-chart] .chart-col');
  for (var i = 0; i < charts.length; i++) {
    charts[i].tabIndex = 0;
  }
  document.addEventListener('focusin', function (e) {
    var col = e.target.closest ? e.target.closest('.chart-col') : null;
    if (col) {
      showTip(col);
    } else {
      hideTip();
    }
  });
})();
