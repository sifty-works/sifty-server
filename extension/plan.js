var SiftyPlan = (() => {
  const norm = (s) => String(s == null ? '' : s).replace(/\s+/g, ' ').trim().toLowerCase();

  const CONSENT = /consent|privacy|terms|gdpr|personal data|data protection|processing of|process (my|your|the) |i agree|i accept|i have read|certify|acknowledge/;
  const SUBMIT_NAME = /(^|[\s_-])submit(?!ted)/i;
  const YES = /^(yes|y|true|1)$/;
  const NO = /^(no|n|false|0)$/;
  const TEXT = new Set(['text', 'email', 'tel', 'url', 'number', 'textarea', 'date']);

  const isConsent = (f) => CONSENT.test(norm([f.label].concat(f.options.map((o) => o.label)).join(' ')));

  const isSubmitLike = (node) => {
    for (let n = node; n; n = n.parent()) {
      const type = norm(n.attr('type'));
      if (type === 'submit' || type === 'image') return true;
      if (n.tag === 'button' && !n.attr('type') && n.hasForm()) return true;
      if (SUBMIT_NAME.test((n.attr('class') || '') + ' ' + (n.attr('id') || ''))) return true;
    }
    return false;
  };

  const hasValue = (f) => norm(f.value) !== '' || (f.values || []).length > 0;

  const match = (options, v) => {
    const w = norm(v);
    const partial = options.filter((o) => w && norm(o.label).includes(w));
    return options.find((o) => norm(o.label) === w) || options.find((o) => norm(o.value) === w) || (partial.length === 1 ? partial[0] : null);
  };

  const formatDate = (v, format) => {
    const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(v).trim());
    if (!m || !format) return v;
    const parts = { yyyy: m[1], yy: m[1].slice(2), mm: m[2], dd: m[3] };
    return format.replace(/yyyy|yy|mm|dd/gi, (t) => parts[t.toLowerCase()]);
  };

  const entryFor = (f, a, leftReason) => {
    const base = { key: f.key, label: f.label };
    if (f.control === 'file') return { ...base, action: 'attach', reason: 'Attach this yourself' };
    if (isConsent(f)) return { ...base, action: 'consent', reason: 'Your consent' };
    if (hasValue(f)) return { ...base, action: 'kept', reason: 'left as the page filled it' };
    if (!a) return leftReason === undefined ? { ...base, action: 'pending' } : { ...base, action: 'left', reason: leftReason };
    const got = { ...base, source: a.source || '', note: a.note || '' };
    const wanted = a.values && a.values.length ? a.values : [a.value];
    const choose = () => ({ ...got, action: 'choose', reason: 'Choose: ' + wanted.join(', ') });
    if (wanted.every((w) => norm(w) === '')) return { ...base, action: 'left', reason: a.note || 'No answer' };
    if (TEXT.has(f.control)) return { ...got, action: 'fill', value: formatDate(a.value, f.format) };
    if (f.control === 'combobox') return { ...got, action: 'combo', value: a.value };
    if (f.control === 'yesno') {
      const v = norm(a.value);
      return YES.test(v) || NO.test(v) ? { ...got, action: 'press', option: YES.test(v) ? 'yes' : 'no', value: a.value } : choose();
    }
    if (f.control === 'checkbox') {
      const v = norm(a.value);
      if (NO.test(v)) return { ...base, action: 'left', reason: 'Answer: No' };
      return { ...got, action: 'check', options: [f.options[0].value], picks: [0], value: a.value };
    }
    const found = wanted.map((w) => match(f.options, w));
    if (found.some((o) => !o)) return choose();
    const picks = found.map((o) => f.options.indexOf(o));
    if (f.control === 'checkboxes') return { ...got, action: 'check', options: found.map((o) => o.value), picks, value: found.map((o) => o.label).join(', ') };
    return { ...got, action: 'select', options: [found[0].value], picks, value: found[0].label };
  };

  const plan = (fields, response) => {
    const answers = new Map((response.answers || []).map((a) => [a.key, a]));
    const left = new Map((response.left || []).map((l) => [l.key, l.reason]));
    return fields.filter((f) => f.control !== 'submit').map((f) => entryFor(f, answers.get(f.key), left.get(f.key)));
  };

  return { plan, isSubmitLike, isConsent, formatDate, norm };
})();

if (typeof module !== 'undefined') module.exports = SiftyPlan;
