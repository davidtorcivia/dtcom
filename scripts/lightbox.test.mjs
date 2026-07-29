// Exercises the article lightbox.
//
//   node scripts/lightbox.test.mjs [static/app.js]
//
// Same approach as editor-shortcuts.test.mjs: no toolchain, no dependencies,
// just the real static/app.js run against a DOM shim so the shipped measuring
// and marking logic is what gets tested rather than a copy of it.
//
// The interesting part is which images become clickable. Offering a zoom on an
// image already at its natural size is a promise the lightbox cannot keep, and
// the hidden half of a light/dark pair has no layout width at all — it must not
// be mistaken for one with infinite room to grow.
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';

const target = process.argv[2] || fileURLToPath(new URL('../static/app.js', import.meta.url));
const src = fs.readFileSync(target, 'utf8');

let pass = 0, fail = 0;
function check(name, got, want) {
  const ok = JSON.stringify(got) === JSON.stringify(want);
  if (ok) { pass++; console.log(`  ok   ${name}`); }
  else { fail++; console.log(`  FAIL ${name}\n       got  ${JSON.stringify(got)}\n       want ${JSON.stringify(want)}`); }
}

// parseMarkup turns the small, known innerHTML strings app.js assigns into
// child elements. Not a general HTML parser — just enough that querySelector
// can find the pieces the code goes looking for afterwards.
function parseMarkup(html, parent) {
  const tagRe = /<(\w+)([^>]*)>/g;
  let m;
  while ((m = tagRe.exec(html)) !== null) {
    const [, tag, attrs] = m;
    const child = makeEl(tag);
    const cls = /class="([^"]*)"/.exec(attrs);
    if (cls) cls[1].split(/\s+/).forEach((c) => c && child._classes.add(c));
    const type = /aria-label="([^"]*)"/.exec(attrs);
    if (type) child.setAttribute('aria-label', type[1]);
    parent.appendChild(child);
  }
}

function makeEl(tag = 'div') {
  const el = {
    tagName: tag.toUpperCase(),
    _attrs: {}, _listeners: {}, _classes: new Set(),
    children: [], parent: null,
    textContent: '', hidden: false, src: '', alt: '',
    naturalWidth: 0, clientWidth: 0,
    // <dialog> API, so the real showModal()/close() calls are observable.
    showModal() { el._modalOpen = true; },
    close() { el._modalOpen = false; el.fire('close'); },
    style: {},
    classList: {
      add(c) { el._classes.add(c); },
      remove(c) { el._classes.delete(c); },
      contains(c) { return el._classes.has(c); },
      toggle(c, on) { if (on) el._classes.add(c); else el._classes.delete(c); },
    },
    setAttribute(k, v) { el._attrs[k] = String(v); },
    getAttribute(k) { return k in el._attrs ? el._attrs[k] : null; },
    removeAttribute(k) { delete el._attrs[k]; },
    hasAttribute(k) { return k in el._attrs; },
    addEventListener(t, fn) { (el._listeners[t] ||= []).push(fn); },
    removeEventListener() {},
    appendChild(c) { el.children.push(c); c.parent = el; return c; },
    focus() { el._focused = true; },
    closest(sel) {
      let n = el;
      while (n) {
        if (sel === 'figure' && n.tagName === 'FIGURE') return n;
        n = n.parent;
      }
      return null;
    },
    querySelector(sel) { return el._find(sel)[0] || null; },
    querySelectorAll(sel) { return el._find(sel); },
    _find(sel) {
      const out = [];
      const want = sel.replace(/^\./, '');
      const byClass = sel.startsWith('.');
      (function walk(n) {
        for (const c of n.children) {
          if (byClass ? c._classes.has(want) : c.tagName === sel.toUpperCase()) out.push(c);
          walk(c);
        }
      })(el);
      return out;
    },
    fire(type, ev = {}) {
      for (const fn of el._listeners[type] || []) fn({ target: el, preventDefault() {}, ...ev });
    },
  };
  Object.defineProperty(el, 'innerHTML', {
    get() { return el._html || ''; },
    set(v) { el._html = v; el.children = []; parseMarkup(v, el); },
  });
  return el;
}

function makeImg({ natural, client, alt = '' }) {
  const img = makeEl('img');
  img.naturalWidth = natural;
  img.clientWidth = client;
  img.alt = alt;
  img.setAttribute('alt', alt);
  img.complete = true;
  img.src = '/images/x.png';
  return img;
}

// --- build the page ---------------------------------------------------------
const prose = makeEl('div');
prose._classes.add('article-prose-body');

// A figure with a caption, oversized: the main case.
const fig = makeEl('figure');
const bigImg = makeImg({ natural: 2000, client: 1080, alt: 'alt text' });
const cap = makeEl('figcaption');
cap._classes.add('figcaption');
cap.textContent = 'The sRGB transfer function';
fig.appendChild(bigImg);
fig.appendChild(cap);
prose.appendChild(fig);

// Already at natural size — nothing to zoom into.
const exactImg = makeImg({ natural: 1080, client: 1080, alt: 'exact' });
prose.appendChild(exactImg);

// Hidden: the unused half of a light/dark pair.
const hiddenImg = makeImg({ natural: 2000, client: 0, alt: 'hidden half' });
prose.appendChild(hiddenImg);

// Oversized but with no caption — falls back to alt.
const bareImg = makeImg({ natural: 1600, client: 800, alt: 'just alt' });
prose.appendChild(bareImg);

const body = makeEl('body');
const documentElement = makeEl('html');
const created = [];

const documentShim = {
  body,
  documentElement,
  _listeners: {},
  addEventListener(t, fn) { (this._listeners[t] ||= []).push(fn); if (t === 'DOMContentLoaded') fn(); },
  querySelector(sel) { return sel === '.article-prose-body' ? prose : null; },
  querySelectorAll() { return []; },
  getElementById() { return null; },
  createElement(tag) { const el = makeEl(tag); created.push(el); return el; },
};

const observers = [];
const sandbox = {
  document: documentShim,
  window: {
    addEventListener(t, fn) { (this._l ||= {}), (this._l[t] ||= []).push(fn); },
    matchMedia: () => ({ matches: false, addEventListener() {}, addListener() {} }),
    MutationObserver: class {
      constructor(cb) { this.cb = cb; observers.push(this); }
      observe() {}
      disconnect() {}
    },
    localStorage: { getItem: () => null, setItem() {} },
  },
  navigator: { userAgent: 'Mozilla/5.0', sendBeacon: () => true },
  localStorage: { getItem: () => null, setItem() {} },
  setTimeout, clearTimeout, Blob: class {}, console,
};
sandbox.window.document = documentShim;
sandbox.window.localStorage = sandbox.localStorage;
sandbox.MutationObserver = sandbox.window.MutationObserver;

new Function(...Object.keys(sandbox), src)(...Object.values(sandbox));

// --- assertions -------------------------------------------------------------
console.log('which images become clickable:');
check('oversized image is marked', bigImg.classList.contains('is-zoomable'), true);
check('image at natural size is not', exactImg.classList.contains('is-zoomable'), false);
check('hidden image is not', hiddenImg.classList.contains('is-zoomable'), false);

console.log('keyboard reachability:');
check('zoomable gets tabindex', bigImg.getAttribute('tabindex'), '0');
check('zoomable gets a role', bigImg.getAttribute('role'), 'button');
check('label uses the caption', bigImg.getAttribute('aria-label'), 'View full size: The sRGB transfer function');
check('label falls back to alt', bareImg.getAttribute('aria-label'), 'View full size: just alt');
check('non-zoomable has no tabindex', exactImg.getAttribute('tabindex'), null);
check('non-zoomable has no role', exactImg.getAttribute('role'), null);

console.log('opening:');
check('no dialog before any click', created.filter((e) => e.tagName === 'DIALOG').length, 0);
bigImg.fire('click');
const dialogs = created.filter((e) => e.tagName === 'DIALOG');
check('one dialog created', dialogs.length, 1);
const box = dialogs[0];
check('dialog is in the document', box.parent === body, true);
check('modal was opened', !!box._modalOpen, true);
check('image src carried across', box.querySelector('.lightbox-img').src, '/images/x.png');
// Nothing but the picture: the caption is on the page under the figure, and
// has already been read by the time anyone opens this.
check('no caption in the dialog', box.querySelector('.lightbox-caption'), null);
// The alt still travels even though nothing draws from it — the image in the
// dialog needs its own description for a screen reader.
check('alt carried across', box.querySelector('.lightbox-img').alt, 'alt text');

console.log('a second open reuses the same dialog:');
bareImg.fire('click');
check('still one dialog', created.filter((e) => e.tagName === 'DIALOG').length, 1);
check('alt updated for the new image', box.querySelector('.lightbox-img').alt, 'just alt');

console.log('non-zoomable images do not open it:');
box._modalOpen = false;
exactImg.fire('click');
check('stayed closed', !!box._modalOpen, false);
hiddenImg.fire('click');
check('hidden half stayed closed', !!box._modalOpen, false);

console.log('keyboard opens it:');
bigImg.fire('keydown', { key: 'Enter' });
check('Enter opens', !!box._modalOpen, true);
box._modalOpen = false;
bigImg.fire('keydown', { key: 'a' });
check('an ordinary key does not', !!box._modalOpen, false);

console.log('dismissal:');
box._modalOpen = true;
box.fire('click', { target: box }); // the backdrop is the dialog itself
check('backdrop click closes', !!box._modalOpen, false);
box._modalOpen = true;
box.fire('click', { target: box.querySelector('.lightbox-img') });
check('clicking the image does not close', !!box._modalOpen, true);
box.fire('click', { target: box.querySelector('.lightbox-close') });
check('the close button closes', !!box._modalOpen, false);

console.log('re-measuring when the theme swaps the visible half:');
hiddenImg.clientWidth = 1080; // it just became visible
observers.forEach((o) => o.cb());
check('newly visible half is now clickable', hiddenImg.classList.contains('is-zoomable'), true);

// --- touch gestures ---------------------------------------------------------
//
// A phone shows the image at a fraction of its size, so the zoom is the whole
// point there. These drive the real pointer handlers.

const stage = box.querySelector('.lightbox-stage');
const lbImg = box.querySelector('.lightbox-img');
stage.clientWidth = 400;
stage.clientHeight = 800;
stage.getBoundingClientRect = () => ({ left: 0, top: 0, width: 400, height: 800 });
lbImg.offsetWidth = 400;
lbImg.offsetHeight = 300;

function scaleOf() {
  const m = /scale\(([\d.]+)\)/.exec(lbImg.style.transform || '');
  return m ? Math.round(parseFloat(m[1]) * 100) / 100 : 1;
}
function translateOf() {
  const m = /translate\(([-\d.]+)px,([-\d.]+)px\)/.exec(lbImg.style.transform || '');
  return m ? [Math.round(+m[1]), Math.round(+m[2])] : [0, 0];
}
function pointer(type, id, x, y, kind = 'touch') {
  stage.fire(type, { pointerId: id, clientX: x, clientY: y, pointerType: kind });
}

function reopen() {
  box._modalOpen = false;
  bigImg.fire('click');
}

console.log('pinch to zoom:');
reopen();
check('opens at fit', scaleOf(), 1);
pointer('pointerdown', 1, 150, 400);
pointer('pointerdown', 2, 250, 400);   // 100px apart
pointer('pointermove', 2, 350, 400);   // now 200px apart -> 2x
check('pinching out magnifies', scaleOf(), 2);
pointer('pointermove', 2, 250, 400);   // back to 100px
check('pinching in returns to fit', scaleOf(), 1);
pointer('pointerup', 1, 150, 400);
pointer('pointerup', 2, 250, 400);

console.log('zoom never goes below fit or past the ceiling:');
reopen();
pointer('pointerdown', 1, 190, 400);
pointer('pointerdown', 2, 210, 400);   // 20px apart
pointer('pointermove', 2, 195, 400);   // squeeze far in
check('clamped at 1x', scaleOf(), 1);
pointer('pointermove', 2, 1200, 400);  // yank far out
check('clamped at the ceiling', scaleOf(), 6);
pointer('pointerup', 1, 190, 400);
pointer('pointerup', 2, 1200, 400);

console.log('pan only once zoomed:');
reopen();
pointer('pointerdown', 1, 200, 400);
pointer('pointermove', 1, 260, 400);   // horizontal drag at 1x
check('no horizontal pan at fit', translateOf()[0], 0);
pointer('pointerup', 1, 260, 400);

console.log('swipe down dismisses:');
reopen();
pointer('pointerdown', 1, 200, 200);
pointer('pointermove', 1, 200, 260);   // 60px, under the threshold
check('short drag follows the finger', translateOf()[1], 60);
pointer('pointerup', 1, 200, 260);
check('short drag springs back, stays open', [!!box._modalOpen, translateOf()[1]], [true, 0]);

reopen();
pointer('pointerdown', 1, 200, 200);
pointer('pointermove', 1, 200, 340);   // 140px, past the threshold
pointer('pointerup', 1, 200, 340);
check('long drag closes', !!box._modalOpen, false);

console.log('a mouse drag is not a dismiss gesture:');
reopen();
pointer('pointerdown', 1, 200, 200, 'mouse');
pointer('pointermove', 1, 200, 340, 'mouse');
pointer('pointerup', 1, 200, 340, 'mouse');
check('mouse drag left it open', !!box._modalOpen, true);

console.log('double tap toggles:');
reopen();
pointer('pointerdown', 1, 200, 400);
pointer('pointerup', 1, 200, 400);
pointer('pointerdown', 1, 200, 400);
pointer('pointerup', 1, 200, 400);
check('second tap zooms in', scaleOf(), 2.5);
pointer('pointerdown', 1, 200, 400);
pointer('pointerup', 1, 200, 400);
pointer('pointerdown', 1, 200, 400);
pointer('pointerup', 1, 200, 400);
check('another double tap returns to fit', scaleOf(), 1);

console.log('reopening always starts at fit:');
pointer('pointerdown', 1, 150, 400);
pointer('pointerdown', 2, 250, 400);
pointer('pointermove', 2, 450, 400);
check('zoomed before closing', scaleOf() > 1, true);
pointer('pointerup', 1, 150, 400);
pointer('pointerup', 2, 450, 400);
box.close();
reopen();
check('reopened at fit', scaleOf(), 1);
check('and centred', translateOf(), [0, 0]);

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
