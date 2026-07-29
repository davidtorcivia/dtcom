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
check('caption carried across', box.querySelector('.lightbox-caption').textContent, 'The sRGB transfer function');
check('image src carried across', box.querySelector('.lightbox-img').src, '/images/x.png');

console.log('a second open reuses the same dialog:');
bareImg.fire('click');
check('still one dialog', created.filter((e) => e.tagName === 'DIALOG').length, 1);
check('caption fell back to alt', box.querySelector('.lightbox-caption').textContent, 'just alt');

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

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
