const test = require('node:test');
const assert = require('node:assert/strict');
const { plan } = require('./plan.js');
const { collect } = require('./collect.js');

class Text {
  constructor(s) { this.s = s; this.tag = '#text'; }
  text() { return this.s; }
  textWithout() { return this.s; }
  hasControl() { return false; }
}

class El {
  constructor(tag, attrs, kids) {
    this.tag = tag;
    this.attrs = attrs;
    this.kids = kids.map((k) => (typeof k === 'string' ? new Text(k) : k));
    this.kids.forEach((k) => { k.up = this; });
    this.raw = this;
  }
  attr(n) { return n in this.attrs ? String(this.attrs[n]) : null; }
  parent() { return this.up || null; }
  text() { return this.kids.map((k) => k.text()).join(' '); }
  textWithout(x) { return this === x ? '' : this.kids.map((k) => k.textWithout(x)).join(' '); }
  hasControl() { return ['input', 'select', 'textarea'].includes(this.tag) || this.kids.some((k) => k.hasControl()); }
  prevText() {
    const sibs = this.up.kids;
    for (let i = sibs.indexOf(this) - 1; i >= 0; i--) {
      if (!sibs[i].hasControl() && sibs[i].text().trim()) return sibs[i].text();
    }
    return '';
  }
  all() { return this.kids.flatMap((k) => (k instanceof El ? [k, ...k.all()] : [])); }
  find(tag) { return this.all().find((k) => k.tag === tag) || null; }
  root() { return this.up ? this.up.root() : this; }
  byId(id) { return this.root().all().find((k) => k.attrs.id === id) || null; }
  labelsFor(id) { return this.root().all().filter((k) => k.tag === 'label' && k.attrs.for === id); }
  visible() { return !this.attrs.hiddenBox; }
  value() { return this.attrs.value || ''; }
  checked() { return !!this.attrs.checked; }
  hasForm() { return this.parent() !== null && (this.parent().tag === 'form' || this.parent().hasForm()); }
  options() {
    return this.kids.filter((k) => k.tag === 'option').map((o) => ({ value: o.attrs.value, label: o.text(), selected: !!o.attrs.selected }));
  }
}

const h = (tag, attrs = {}, ...kids) => new El(tag, attrs, kids);
const docOf = (root) => ({
  controls: () => root.all().filter((n) => ['input', 'textarea', 'select', 'button'].includes(n.tag) || n.attrs.role === 'combobox'),
});

const field = (key, control, label, extra = {}) => ({ key, control, label, required: false, options: [], value: '', ...extra });

test('fill plan never touches a file, a consent box, a prefilled field or a submit control', () => {
  const yesNo = [{ value: 'yes', label: 'Yes' }, { value: 'no', label: 'No' }];
  const fields = [
    field('name', 'text', 'Full name'),
    field('email', 'email', 'Email'),
    field('start', 'date', 'Start date', { format: 'DD.MM.YYYY' }),
    field('city', 'text', 'City', { value: 'Lisbon' }),
    field('cv', 'file', 'Resume'),
    field('go', 'submit', 'Submit application'),
    field('auth', 'radio', 'Work authorisation', { options: yesNo }),
    field('country', 'select', 'Country', { options: [{ value: 'pt', label: 'Portugal' }, { value: 'de', label: 'Germany' }] }),
    field('remote', 'yesno', 'Remote?', { options: yesNo }),
    field('office', 'combobox', 'Office'),
    field('c1', 'checkbox', 'I consent to being contacted', { options: [{ value: 'y', label: 'Yes' }] }),
    field('c2', 'checkbox', 'Read the privacy policy', { options: [{ value: 'y', label: 'Yes' }] }),
    field('c3', 'checkbox', 'Processing of personal data', { options: [{ value: 'y', label: 'Yes' }] }),
    field('c4', 'checkbox', 'Newsletter', { options: [{ value: 'y', label: 'I agree to the terms' }] }),
  ];
  const answers = fields.map((f) => ({ key: f.key, value: { start: '2026-03-09', country: 'Germany', remote: 'No', auth: 'Yes' }[f.key] || 'Yes', source: 'My data', note: '' }));
  const byKey = Object.fromEntries(plan(fields, { answers, left: [] }).map((e) => [e.key, e]));
  const doing = new Set(['fill', 'select', 'check', 'press', 'combo']);

  for (const key of ['cv', 'city', 'c1', 'c2', 'c3', 'c4']) assert.ok(!doing.has(byKey[key].action), key + ' must not be filled');
  assert.equal(byKey.go, undefined);
  assert.equal(byKey.cv.reason, 'Attach this yourself');
  assert.equal(byKey.c1.reason, 'Your consent');
  assert.equal(byKey.city.reason, 'left as the page filled it');
  assert.deepEqual([byKey.name.action, byKey.name.value], ['fill', 'Yes']);
  assert.equal(byKey.start.value, '09.03.2026');
  assert.deepEqual([byKey.country.action, byKey.country.options], ['select', ['de']]);
  assert.deepEqual([byKey.remote.action, byKey.remote.option], ['press', 'no']);
  assert.equal(byKey.office.action, 'combo');

  const pairIn = (wrapper, attrs) => {
    const root = h('div', {}, wrapper(h('div', {}, h('label', {}, 'Relocate?'), h('div', {}, h('button', attrs, 'Yes'), h('button', attrs, 'No')))));
    return collect(docOf(root)).fields.map((f) => f.kind);
  };
  assert.deepEqual(pairIn((d) => h('form', {}, d), {}), []);
  assert.deepEqual(pairIn((d) => d, { type: 'submit' }), []);
  assert.deepEqual(pairIn((d) => h('div', { class: 'application--submit' }, d), { type: 'button' }), []);
  assert.deepEqual(pairIn((d) => d, {}), ['yes_no']);
});

test('collector reads labels, required marks, kinds and options', () => {
  const opt = (value, label, extra = {}) => h('option', { value, ...extra }, label);
  const root = h('div', {},
    h('form', {},
      h('label', { for: 'first' }, 'First name', h('span', {}, '*')),
      h('input', { id: 'first', type: 'text' }),
      h('label', {}, 'Email', h('input', { type: 'email', name: 'email' })),
      h('label', {}, 'Country', h('select', { name: 'country' }, opt('', 'Select ...'), opt('de', 'Germany'), opt('fr', 'France'))),
      h('span', { id: 'portfolio-label' }, 'Portfolio link'),
      h('input', { id: 'portfolio', type: 'text', 'aria-labelledby': 'portfolio-label' }),
      h('fieldset', {},
        h('legend', {}, 'Work authorisation'),
        h('label', {}, h('input', { type: 'radio', name: 'auth', value: 'yes' }), h('span', {}, 'Yes')),
        h('label', {}, h('input', { type: 'radio', name: 'auth', value: 'no', checked: true }), h('span', {}, 'No'))),
      h('ul', {},
        h('li', {}, h('div', { class: 'label' }, 'Nickname'), h('div', {}, h('input', { type: 'text', name: 'nick' }))),
        h('li', {}, h('div', { class: 'label' }, 'Referred by?', h('span', {}, '✱')), h('div', {}, h('input', { type: 'text', name: 'ref' })))),
      h('label', {}, 'Resume', h('input', { type: 'file', name: 'cv' })),
      h('input', { type: 'hidden', name: 'token', value: 'x' }),
      h('input', { type: 'submit', name: 'send', value: 'Send' }),
      h('input', { type: 'text', name: 'trap', 'aria-hidden': 'true' }),
      h('input', { type: 'text', name: 'offscreen', hiddenBox: true }),
      h('button', { type: 'submit' }, 'Apply')));
  const { fields } = collect(docOf(root));
  const by = Object.fromEntries(fields.map((f) => [f.key, f]));

  assert.deepEqual(fields.map((f) => f.key), ['first', 'email', 'country', 'portfolio', 'auth', 'nick', 'ref', 'cv']);
  assert.deepEqual([by.first.label, by.first.required, by.first.kind], ['First name', true, 'short_text']);
  assert.deepEqual([by.email.label, by.email.kind], ['Email', 'email']);
  assert.equal(by.country.label, 'Country');
  assert.deepEqual(by.country.options, [{ value: 'de', label: 'Germany' }, { value: 'fr', label: 'France' }]);
  assert.equal(by.portfolio.label, 'Portfolio link');
  assert.equal(by.auth.label, 'Work authorisation');
  assert.deepEqual(by.auth.options, [{ value: 'yes', label: 'Yes' }, { value: 'no', label: 'No' }]);
  assert.deepEqual([by.auth.kind, by.auth.value], ['choice', 'No']);
  assert.equal(by.cv.kind, 'file');
  assert.deepEqual([by.nick.label, by.nick.required], ['Nickname', false]);
  assert.deepEqual([by.ref.label, by.ref.required], ['Referred by?', true]);
});
