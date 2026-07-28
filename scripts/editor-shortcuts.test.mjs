// Exercises the editor's formatting shortcuts.
//
//   node scripts/editor-shortcuts.test.mjs static/editor.js
//
// There is no JS toolchain in this repo and this does not add one: it is a
// single dependency-free script that loads the real static/editor.js against a
// minimal DOM shim, so the shipped key bindings and the wrap/unwrap logic are
// what get tested rather than a copy of them.
//
// execCommand is deliberately absent from the shim. That forces the fallback
// splice path, which is the deterministic one — and is also what runs in any
// browser that has dropped execCommand.
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';

// fileURLToPath, not URL.pathname: the latter yields /C:/... with percent-
// escapes on Windows and fails to open.
const target = process.argv[2] || fileURLToPath(new URL('../static/editor.js', import.meta.url));
const src = fs.readFileSync(target, 'utf8');

function makeEl(cls) {
  return {
    _cls: cls, _listeners: {}, classList: { toggle(){}, add(){}, remove(){}, contains(){return false;} },
    textContent: '', hidden: false, innerHTML: '',
    addEventListener(t, fn) { (this._listeners[t] ||= []).push(fn); },
    setAttribute(){}, removeAttribute(){}, focus(){}, click(){},
    querySelector(){ return null; },
  };
}

const ta = makeEl('editor-write');
ta.value = '';
ta.selectionStart = 0;
ta.selectionEnd = 0;
ta.form = null;

const editor = makeEl('editor');
const map = {
  '.editor-write': ta,
  '.editor-preview': makeEl('editor-preview'),
  '[data-mode="write"]': makeEl('w'),
  '[data-mode="preview"]': makeEl('p'),
  '[data-action="image"]': null,
  '.editor-file': null,
  '.editor-status': makeEl('status'),
};
editor.querySelector = (sel) => (sel in map ? map[sel] : null);

const documentShim = {
  _listeners: {},
  addEventListener(t, fn) { (this._listeners[t] ||= []).push(fn); if (t === 'DOMContentLoaded') fn(); },
  querySelector: (sel) => (sel === '.editor' ? editor : null),
  createElement: () => makeEl('div'),
  // Force the fallback splice path: jsdom-less Node has no execCommand, which
  // is also what a browser without it would do.
  execCommand: undefined,
};

// Uploads go through fetch. Record the calls and hand back what the real
// /admin/images endpoint returns, so the insert path is exercised for real.
const fetches = [];
let uploadResponse = { url: '/images/abc123.png', markdown: '![](/images/abc123.png)' };

const sandbox = {
  document: documentShim,
  window: { addEventListener(){}, location: { pathname: '/admin/posts/x/edit' } },
  fetch: (url, opts) => {
    fetches.push({ url, opts });
    return Promise.resolve({ ok: true, json: () => Promise.resolve(uploadResponse) });
  },
  FormData: class { append() {} },
  console,
};
sandbox.window.document = documentShim;

new Function(...Object.keys(sandbox), src)(...Object.values(sandbox));

function press(key, { shift = false, ctrl = true } = {}) {
  let prevented = false;
  const ev = { key, ctrlKey: ctrl, metaKey: false, shiftKey: shift, altKey: false,
               preventDefault() { prevented = true; } };
  for (const fn of ta._listeners.keydown || []) fn(ev);
  return prevented;
}

function setSel(value, start, end) {
  ta.value = value; ta.selectionStart = start; ta.selectionEnd = end;
}
function state() {
  return { v: ta.value, s: ta.selectionStart, e: ta.selectionEnd,
           sel: ta.value.slice(ta.selectionStart, ta.selectionEnd) };
}

let pass = 0, fail = 0;
function check(name, got, want) {
  const ok = JSON.stringify(got) === JSON.stringify(want);
  if (ok) { pass++; console.log(`  ok   ${name}`); }
  else { fail++; console.log(`  FAIL ${name}\n       got  ${JSON.stringify(got)}\n       want ${JSON.stringify(want)}`); }
}

console.log('bold:');
setSel('hello world', 0, 5); press('b');
check('wraps selection', state().v, '**hello** world');
check('keeps inner selected', state().sel, 'hello');

press('b');
check('second press unwraps', state().v, 'hello world');
check('selection preserved', state().sel, 'hello');

console.log('italic / code / strike / highlight:');
setSel('abc', 0, 3); press('i');
check('italic', state().v, '*abc*');
setSel('abc', 0, 3); press('e');
check('code', state().v, '`abc`');
setSel('abc', 0, 3); press('x', { shift: true });
check('strikethrough', state().v, '~~abc~~');
setSel('abc', 0, 3); press('h', { shift: true });
check('highlight', state().v, '==abc==');

console.log('no selection:');
setSel('', 0, 0); press('b');
check('inserts placeholder', state().v, '**bold**');
check('placeholder selected', state().sel, 'bold');

console.log('unwrap when markers are inside the selection:');
setSel('**hey**', 0, 7); press('b');
check('strips markers', state().v, 'hey');

console.log('link:');
setSel('docs', 0, 4); press('k');
check('builds link', state().v, '[docs]()');
check('caret inside parens', [state().s, state().e], [7, 7]);
setSel('', 0, 0); press('k');
check('empty link', state().v, '[text]()');

console.log('mid-document, offsets respected:');
setSel('a hello b', 2, 7); press('b');
check('wraps in place', state().v, 'a **hello** b');
press('b');
check('unwraps in place', state().v, 'a hello b');

console.log('nesting bold inside italic:');
setSel('word', 0, 4); press('i'); press('b');
check('layers', state().v, '***word***');

console.log('non-shortcut keys ignored:');
setSel('abc', 0, 3);
check('ctrl+q not handled', press('q'), false);
check('unchanged', state().v, 'abc');
check('plain b not handled', press('b', { ctrl: false }), false);
check('still unchanged', state().v, 'abc');

// --- image upload: drop, paste, and where the caret lands -------------------
//
// The caret position is the point. An image alone in its paragraph renders as a
// <figure> whose caption is its alt text, so after an upload the caret has to
// sit inside the empty ![] brackets — otherwise every dropped image ships
// without a caption unless the author goes back and clicks between them.

function fileOfType(type, name) {
  return { type, name };
}

function fireDrop(files) {
  const ev = {
    dataTransfer: { files, types: ['Files'] },
    preventDefault() {},
  };
  for (const fn of ta._listeners.drop || []) fn(ev);
}

function firePaste(items) {
  const ev = { clipboardData: { items }, preventDefault() {} };
  for (const fn of ta._listeners.paste || []) fn(ev);
}

// The upload chain is promise-based; let the microtasks drain.
const settle = () => new Promise((r) => setTimeout(r, 0));

console.log('image drop:');
fetches.length = 0;
setSel('Before. After.', 8, 8);
fireDrop([fileOfType('image/png', 'shot.png')]);
await settle();
check('posts to /admin/images', fetches.map((f) => f.url), ['/admin/images']);
check('sends credentials', fetches[0]?.opts?.credentials, 'same-origin');
check('inserts the markdown', state().v, 'Before. ![](/images/abc123.png)After.');
check('caret lands inside the alt brackets', [state().s, state().e], [10, 10]);

// Typing straight after the drop must produce a caption.
setSel(state().v.slice(0, state().s) + 'A wide shot' + state().v.slice(state().s), 0, 0);
check('typed text becomes the alt', state().v, 'Before. ![A wide shot](/images/abc123.png)After.');

console.log('non-image drop ignored:');
fetches.length = 0;
setSel('', 0, 0);
fireDrop([fileOfType('application/pdf', 'paper.pdf')]);
await settle();
check('no upload attempted', fetches.length, 0);
check('buffer untouched', state().v, '');

console.log('image paste:');
fetches.length = 0;
setSel('', 0, 0);
firePaste([{ kind: 'file', type: 'image/jpeg', getAsFile: () => fileOfType('image/jpeg', 'p.jpg') }]);
await settle();
check('uploads pasted image', fetches.length, 1);
check('caret inside the brackets', [state().s, state().e], [2, 2]);

console.log('pasting plain text is not an upload:');
fetches.length = 0;
firePaste([{ kind: 'string', type: 'text/plain', getAsFile: () => null }]);
await settle();
check('no upload attempted', fetches.length, 0);

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
