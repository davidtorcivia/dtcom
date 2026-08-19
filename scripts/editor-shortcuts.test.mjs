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
    textContent: '', hidden: false, innerHTML: '', value: '', checked: false,
    // The editor sizes the textarea to its content on every keystroke; without
    // a style object to write into, every input event would throw.
    style: {}, scrollHeight: 0,
    addEventListener(t, fn) { (this._listeners[t] ||= []).push(fn); },
    setAttribute(){}, removeAttribute(){}, focus(){}, click(){},
    querySelector(){ return null; },
  };
}

// fire dispatches an event to whatever the editor bound for it, and reports
// whether the handler took the key.
function fire(el, type, ev = {}) {
  let prevented = false;
  const event = { preventDefault() { prevented = true; }, ...ev };
  for (const fn of el._listeners[type] || []) fn(event);
  return prevented;
}

const ta = makeEl('editor-write');
ta.value = '';
ta.selectionStart = 0;
ta.selectionEnd = 0;
ta.form = null;

const editor = makeEl('editor');

// The frontmatter fields and the form around them: the editor treats all of it
// as one draft, so the stash and the restore prompt cannot be exercised
// without them.
const slugField = makeEl('slug');
slugField.value = 'my-post';
const formFields = {
  '[name="slug"]': slugField,
  '[name="title"]': makeEl('title'),
  '[name="date"]': makeEl('date'),
  '[name="description"]': makeEl('description'),
  '[name="tags"]': makeEl('tags'),
  '[name="draft"]': makeEl('draft'),
};
formFields['[name="title"]'].value = 'My Post';

const form = makeEl('form');
form.action = '/admin/posts/save';
form.querySelector = (sel) => formFields[sel] ?? null;
form.requestSubmit = () => fire(form, 'submit');
ta.form = form;

const restoreBar = makeEl('restore');
const restoreText = makeEl('restore-text');
restoreBar.querySelector = (sel) => (sel === '.editor-restore-text' ? restoreText : null);

const countEl = makeEl('count');
const statusEl = makeEl('status');
const pairFile = makeEl('pair-file');

const map = {
  '.editor-write': ta,
  '.editor-preview': makeEl('editor-preview'),
  '[data-mode="write"]': makeEl('w'),
  '[data-mode="preview"]': makeEl('p'),
  '[data-action="image"]': null,
  '.editor-file': null,
  '[data-action="pair"]': makeEl('pair'),
  '.editor-pair-file': pairFile,
  '.editor-status': statusEl,
  '.editor-count': countEl,
  '.editor-restore': restoreBar,
};
editor.querySelector = (sel) => (sel in map ? map[sel] : null);

// A draft this browser is holding that the server did not render — seeded
// before the editor loads, which is exactly when a real one would be found.
const STASH_KEY = 'dtcom-draft:my-post';
const stashed = {
  body: 'recovered paragraph', title: 'My Post', date: '', description: '', tags: '',
  draft: false, t: 1700000000000,
};
const storage = new Map([[STASH_KEY, JSON.stringify(stashed)]]);
const localStorageShim = {
  getItem: (k) => (storage.has(k) ? storage.get(k) : null),
  setItem: (k, v) => storage.set(k, v),
  removeItem: (k) => storage.delete(k),
};

const documentShim = {
  _listeners: {},
  addEventListener(t, fn) { (this._listeners[t] ||= []).push(fn); if (t === 'DOMContentLoaded') fn(); },
  querySelector: (sel) => (sel === '.editor' ? editor : null),
  createElement: () => makeEl('div'),
  // Force the fallback splice path: jsdom-less Node has no execCommand, which
  // is also what a browser without it would do.
  execCommand: undefined,
};

// Uploads and saves go through fetch. Record the calls and hand back what the
// real endpoints return, so both insert paths are exercised for real.
const fetches = [];
const uploadedURL = (name) => '/images/' + String(name || 'upload').replace(/\.[^.]+$/, '') + '-hash.png';
let saveResponse = { ok: true, status: 200, body: { slug: 'my-post' } };

const windowShim = {
  _listeners: {},
  addEventListener(t, fn) { (this._listeners[t] ||= []).push(fn); },
  location: { pathname: '/admin/posts/x/edit' },
  localStorage: localStorageShim,
};

const sandbox = {
  document: documentShim,
  window: windowShim,
  fetch: (url, opts) => {
    fetches.push({ url, opts });
    if (url === '/admin/posts/save') {
      return Promise.resolve({
        ok: saveResponse.ok,
        status: saveResponse.status,
        redirected: !!saveResponse.redirected,
        json: () => (saveResponse.body
          ? Promise.resolve(saveResponse.body)
          : Promise.reject(new Error('not JSON'))),
      });
    }
    const file = opts?.body?.parts?.find((p) => p.key === 'file');
    const stored = uploadedURL(file?.name);
    return Promise.resolve({
      ok: true, status: 201, redirected: false,
      json: () => Promise.resolve({ url: stored, markdown: `![](${stored})` }),
    });
  },
  FormData: class {
    constructor() { this.parts = []; }
    append(key, value, name) { this.parts.push({ key, value, name }); }
  },
  console,
};
sandbox.window.document = documentShim;

new Function(...Object.keys(sandbox), src)(...Object.values(sandbox));

function press(key, { shift = false, ctrl = true, alt = false, code = '' } = {}) {
  return fire(ta, 'keydown', { key, code, ctrlKey: ctrl, metaKey: false,
                               shiftKey: shift, altKey: alt });
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
check('inserts the markdown', state().v, 'Before. ![](/images/shot-hash.png)After.');
check('caret lands inside the alt brackets', [state().s, state().e], [10, 10]);

// Typing straight after the drop must produce a caption.
setSel(state().v.slice(0, state().s) + 'A wide shot' + state().v.slice(state().s), 0, 0);
check('typed text becomes the alt', state().v, 'Before. ![A wide shot](/images/shot-hash.png)After.');

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

// --- the light/dark pair ---------------------------------------------------
//
// The two lines have to end up in ONE paragraph: that is what the renderer
// collapses into a single figure that swaps with the theme. A blank line
// between them, and the reader gets two images stacked instead.

function pickPair(files) {
  pairFile.files = files;
  fire(pairFile, 'change');
}

console.log('light/dark pair:');
fetches.length = 0;
setSel('', 0, 0);
pickPair([fileOfType('image/png', 'fig-light.png'), fileOfType('image/png', 'fig-dark.png')]);
await settle();
check('uploads both', fetches.map((f) => f.url), ['/admin/images', '/admin/images']);
check('one paragraph, tagged, caption on the first', state().v,
      '![](/images/fig-light-hash.png#light)\n![](/images/fig-dark-hash.png#dark)');
check('caret inside the first alt brackets', [state().s, state().e], [2, 2]);

console.log('the file dialog hands back its own order, so the names decide:');
setSel('', 0, 0);
pickPair([fileOfType('image/png', 'diagram-dark.png'), fileOfType('image/png', 'diagram-light.png')]);
await settle();
check('reversed selection still tagged correctly', state().v,
      '![](/images/diagram-light-hash.png#light)\n![](/images/diagram-dark-hash.png#dark)');

console.log('unnamed files fall back to the order they arrived in:');
setSel('', 0, 0);
pickPair([fileOfType('image/png', 'a.png'), fileOfType('image/png', 'b.png')]);
await settle();
check('first is light', state().v, '![](/images/a-hash.png#light)\n![](/images/b-hash.png#dark)');
check('says which is which', statusEl.textContent,
      'a.png is the light one, b.png the dark one — type a caption');

// The status line after a pair is the only place that says which file became
// the dark one. The autosave note fires a second later and must not take that
// answer away before it has been read.
console.log('the autosave note does not stomp the upload message:');
await new Promise((r) => setTimeout(r, 1200));
check('upload message survives the debounce', statusEl.textContent,
      'a.png is the light one, b.png the dark one — type a caption');
fire(ta, 'input');
await new Promise((r) => setTimeout(r, 1200));
check('but it does upgrade its own', statusEl.textContent,
      'Unsaved changes — draft kept in this browser');

console.log('a pair needs exactly two images:');
fetches.length = 0;
setSel('untouched', 9, 9);
pickPair([fileOfType('image/png', 'only-one.png')]);
await settle();
check('no upload attempted', fetches.length, 0);
check('buffer untouched', state().v, 'untouched');
check('says what is wrong', statusEl.textContent, 'Pick exactly two images: the light one and the dark one.');

fetches.length = 0;
pickPair([fileOfType('image/png', 'a.png'), fileOfType('application/pdf', 'paper.pdf')]);
await settle();
check('non-images do not count towards the two', fetches.length, 0);

console.log('pasting plain text is not an upload:');
fetches.length = 0;
firePaste([{ kind: 'string', type: 'text/plain', getAsFile: () => null }]);
await settle();
check('no upload attempted', fetches.length, 0);

// --- word count and height -------------------------------------------------

console.log('word count:');
setSel('one two  three', 0, 0);
fire(ta, 'input');
check('counts words', countEl.textContent, '3 words');
setSel('solo', 0, 0);
fire(ta, 'input');
check('singular', countEl.textContent, '1 word');
check('grows to fit its content', typeof ta.style.height, 'string');

// --- Enter continues the structure it is inside ----------------------------
//
// The indent capture is the part that matters: a continued item has to land at
// the depth of the one above it, or every nested list has to be re-indented by
// hand.

console.log('list continuation:');
setSel('- one', 5, 5);
check('bullet takes Enter', press('Enter', { ctrl: false }), true);
check('opens the next item', state().v, '- one\n- ');
check('caret after the marker', [state().s, state().e], [8, 8]);

setSel('3. three', 8, 8); press('Enter', { ctrl: false });
check('ordered increments', state().v, '3. three\n4. ');

setSel('- [x] done', 10, 10); press('Enter', { ctrl: false });
check('task box comes back unticked', state().v, '- [x] done\n- [ ] ');

setSel('- one\n  - two', 13, 13); press('Enter', { ctrl: false });
check('nested item keeps its depth', state().v, '- one\n  - two\n  - ');

setSel('> quoted', 8, 8); press('Enter', { ctrl: false });
check('blockquote continues', state().v, '> quoted\n> ');

console.log('Enter on an empty item steps back out:');
setSel('- a\n  - ', 8, 8); press('Enter', { ctrl: false });
check('nested item outdents', state().v, '- a\n- ');
check('caret after the marker', [state().s, state().e], [6, 6]);

setSel('- a\n- ', 6, 6); press('Enter', { ctrl: false });
check('flush item drops its marker', state().v, '- a\n');
check('caret on the empty line', [state().s, state().e], [4, 4]);

console.log('Enter left alone elsewhere:');
setSel('a paragraph', 11, 11);
check('plain text not handled', press('Enter', { ctrl: false }), false);
check('unchanged', state().v, 'a paragraph');
setSel('- one', 5, 5);
check('shift+enter not handled', press('Enter', { ctrl: false, shift: true }), false);
check('unchanged', state().v, '- one');

// --- Tab nests -------------------------------------------------------------
//
// CommonMark nests by the column the parent's text starts in, so the indent
// has to come from the item above rather than from a fixed two spaces: under
// "1. " a two-space indent is a sibling, not a child.

console.log('Tab nesting:');
setSel('- one\n- two', 11, 11);
check('bullet parent gives two columns', press('Tab', { ctrl: false }), true);
check('indented', state().v, '- one\n  - two');
check('caret follows', [state().s, state().e], [13, 13]);

setSel('1. one\n- two', 12, 12); press('Tab', { ctrl: false });
check('ordered parent gives three', state().v, '1. one\n   - two');

setSel('1. one\n   - two', 15, 15);
check('shift+tab outdents', press('Tab', { ctrl: false, shift: true }), true);
check('back to the parent depth', state().v, '1. one\n- two');

setSel('- one', 5, 5);
check('flush left is not outdentable', press('Tab', { ctrl: false, shift: true }), false);
setSel('a paragraph', 5, 5);
check('Tab still moves focus outside a list', press('Tab', { ctrl: false }), false);
check('unchanged', state().v, 'a paragraph');

// --- headings --------------------------------------------------------------
//
// Alt is part of the chord because Ctrl/Cmd+1..6 alone is the browser's own
// switch-to-tab-N, and the digit comes from e.code because holding Option on a
// Mac rewrites e.key to the character it would type.

console.log('headings:');
setSel('foo', 3, 3);
check('alt+2 marks a heading', press('2', { alt: true, code: 'Digit2' }), true);
check('marker added', state().v, '## foo');
press('2', { alt: true, code: 'Digit2' });
check('same level removes it', state().v, 'foo');
press('2', { alt: true, code: 'Digit2' });
press('4', { alt: true, code: 'Digit4' });
check('another level replaces it', state().v, '#### foo');
check('bare alt+7 ignored', press('7', { alt: true, code: 'Digit7' }), false);

// --- saving in place -------------------------------------------------------
//
// A new post has no slug until the server derives one. Writing it back into
// the hidden field is what makes the second save an update of the file the
// first one created, rather than a create that collides with it.

console.log('save in place:');
fetches.length = 0;
slugField.value = '';
saveResponse = { ok: true, status: 200, body: { slug: 'derived-slug' } };
setSel('the post', 0, 0);
fire(form, 'submit');
await settle();
check('posts to the save endpoint', fetches.map((f) => f.url), ['/admin/posts/save']);
check('asks for JSON', fetches[0]?.opts?.headers?.Accept, 'application/json');
check('sends credentials', fetches[0]?.opts?.credentials, 'same-origin');
check('slug written back for the next save', slugField.value, 'derived-slug');
check('reports when it saved', /^Saved \d\d:\d\d/.test(statusEl.textContent), true);

console.log('an expired session does not swallow the draft:');
saveResponse = { ok: false, status: 401, body: {} };
fire(form, 'submit');
await settle();
check('says so', statusEl.textContent, 'Session expired — sign in again in another tab, then Save.');
check('body untouched', state().v, 'the post');

// static/ is bind-mounted into the container, so this script can be a version
// ahead of the binary answering it — and that binary redirects to the post list
// instead of returning JSON. fetch follows the redirect and reports a perfectly
// successful 200 of the wrong page. Reading that as success would clear the
// stash and tell the author their work was saved.
console.log('a followed redirect is not a save:');
storage.set(STASH_KEY, JSON.stringify({ body: 'held', title: '', date: '', description: '', tags: '', draft: false, t: 1 }));
saveResponse = { ok: true, status: 200, redirected: true, body: null };
fire(form, 'submit');
await settle();
check('reported, not swallowed', statusEl.textContent,
      'Session expired — sign in again in another tab, then Save.');
check('stash left alone', !!storage.get(STASH_KEY), true);
storage.delete(STASH_KEY);

console.log('a rejected save shows the reason:');
saveResponse = { ok: false, status: 400, body: { error: 'title is required' } };
fire(form, 'submit');
await settle();
check('server message shown', statusEl.textContent, 'title is required');

// --- the stash -------------------------------------------------------------

console.log('unsaved work is flushed on unload:');
slugField.value = 'my-post';
storage.delete(STASH_KEY);
setSel('a sentence that never reached the server', 0, 0);
fire(windowShim, 'beforeunload', { returnValue: '' });
check('stashed under the slug', JSON.parse(storage.get(STASH_KEY)).body,
      'a sentence that never reached the server');

console.log('restore prompt:');
check('offered at load', restoreBar.hidden, false);
check('says when', /^Unsaved changes from .+ are still here\.$/.test(restoreText.textContent), true);
fire(restoreBar, 'click', { target: { getAttribute: (n) => (n === 'data-action' ? 'restore' : null) } });
check('puts the draft back', state().v, 'recovered paragraph');
check('prompt dismissed', restoreBar.hidden, true);

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
