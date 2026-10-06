// Test harness for templates/app.js: a tiny fake DOM, no dependencies.
// Usage: node harness.js <app.js> < input.json > output.json
// Input: {catalog: {...}, hash: "tag=x", click: ["Overlaps"]}
'use strict';
const fs = require('fs');
const vm = require('vm');

class El {
  constructor(tag) {
    this.tagName = String(tag).toUpperCase();
    this.children = [];
    this.attrs = {};
    this.listeners = {};
    this.className = '';
    this._text = '';
    this._value = '';
  }
  get firstChild() { return this.children[0] || null; }
  appendChild(n) { this.children.push(n); return n; }
  removeChild(n) { this.children = this.children.filter((c) => c !== n); return n; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  addEventListener(ev, fn) { (this.listeners[ev] = this.listeners[ev] || []).push(fn); }
  focus() {}
  get textContent() { return this._text + this.children.map((c) => c.textContent).join(''); }
  set textContent(v) { this.children = []; this._text = String(v); }
  get value() { return this._value; }
  set value(v) {
    if (this.tagName === 'SELECT' && v !== '' && !this.children.some((o) => o._value === v)) { this._value = ''; return; }
    this._value = String(v);
  }
}

const ids = {};
function byId(id) { return ids[id] || (ids[id] = new El(id.startsWith('f-') ? 'select' : 'div')); }

function walk(node, fn) { fn(node); node.children.forEach((c) => walk(c, fn)); }

const input = JSON.parse(fs.readFileSync(0, 'utf8'));
byId('catalog-data')._text = JSON.stringify(input.catalog);
const document = {
  title: 'x',
  getElementById: byId,
  createElement: (t) => new El(t),
  createTextNode: (t) => { const e = new El('#text'); e._text = String(t); return e; },
  addEventListener() {},
};
const window = {
  location: { hash: input.hash ? '#' + input.hash : '', pathname: '/', search: '' },
  history: { replaceState() {} },
  addEventListener() {},
};
const sandbox = { document, window, URL, URLSearchParams, console };
vm.runInNewContext(fs.readFileSync(process.argv[2], 'utf8'), sandbox, { timeout: 10000 });

const out = { selects: {}, count: '', meta: '', anchors: [], headings: [], clusters: [], cards: [] };
function collect() {
  out.count = byId('count').textContent;
  out.anchors = []; out.headings = []; out.clusters = []; out.cards = [];
  walk(byId('results'), (n) => {
    if (n.tagName === 'A') { out.anchors.push(n.attrs.href); }
    if (n.tagName === 'H2') { out.headings.push(n.textContent); }
    if (n.className === 'cluster') { out.clusters.push(n.children[0].textContent); }
  });
}
for (const k of ['category', 'tag', 'status', 'owner']) {
  out.selects[k] = byId('f-' + k).children.map((o) => o._value);
}
out.meta = byId('meta').textContent;
collect();
out.plugins = { headings: out.headings.slice(), anchors: out.anchors.slice(), count: out.count };
for (const label of input.click || []) {
  const tab = byId('tabs').children.find((b) => b.textContent === label);
  tab.listeners.click[0]();
}
collect();
process.stdout.write(JSON.stringify(out));
