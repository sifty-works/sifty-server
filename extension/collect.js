var SiftyPlan = typeof module !== 'undefined' ? require('./plan.js') : SiftyPlan;

var SiftyCollect = (() => {
  const { isSubmitLike, norm } = SiftyPlan;
  const SKIP_TYPES = new Set(['hidden', 'submit', 'button', 'image', 'reset', 'password', 'color', 'range']);
  const TEXT_TYPES = new Set(['email', 'tel', 'url', 'number']);
  const MARK = /\s*([*✱]+|\(required\))\s*$/i;
  const PLACEHOLDER_OPTION = /^(select|choose|please)\b|^[-–—.\s]+$/i;
  const DATE_FORMAT = /^(?=.*(yyyy|yy))(?=.*mm)(?=.*dd).+$/i;

  const squash = (s) => String(s || '').replace(/\s+/g, ' ').trim();
  const cleaned = (raw) => {
    const t = squash(raw).slice(0, 300);
    return { label: t.replace(MARK, '').trim(), required: MARK.test(t) };
  };

  const nearest = (n, pred) => {
    for (let p = n; p; p = p.parent()) if (pred(p)) return p;
    return null;
  };

  const labelledBy = (n) => squash((n.attr('aria-labelledby') || '').split(/\s+/).map((id) => n.byId(id)).filter(Boolean).map((x) => x.text()).join(' '));

  const ownLabel = (n) => {
    const id = n.attr('id');
    const wrapping = nearest(n.parent(), (p) => p.tag === 'label');
    return squash(id ? n.labelsFor(id).map((l) => l.textWithout(n)).join(' ') : '') ||
      squash(wrapping ? wrapping.textWithout(n) : '') ||
      labelledBy(n) ||
      squash(n.attr('aria-label'));
  };

  const preceding = (start) => {
    let p = start;
    for (let i = 0; i < 5 && p; i++, p = p.parent()) {
      const t = squash(p.prevText());
      if (t) return t;
    }
    return '';
  };

  const groupLabel = (start) => {
    let p = start;
    for (let i = 0; i < 6 && p; i++, p = p.parent()) {
      const role = norm(p.attr('role'));
      if (p.tag !== 'fieldset' && role !== 'radiogroup' && role !== 'group') continue;
      const legend = p.tag === 'fieldset' ? p.find('legend') : null;
      const t = labelledBy(p) || squash(p.attr('aria-label')) || (legend ? squash(legend.text()) : '');
      if (t) return t;
    }
    return preceding(start);
  };

  const controlLabel = (n) => {
    const fieldset = nearest(n, (p) => p.tag === 'fieldset');
    const legend = fieldset ? fieldset.find('legend') : null;
    return ownLabel(n) || (legend ? squash(legend.text()) : '') || preceding(n) || squash(n.attr('placeholder'));
  };

  const isRequired = (n, fromLabel) => n.attr('required') !== null || n.attr('aria-required') === 'true' || fromLabel;

  const optionOf = (n) => {
    const own = ownLabel(n);
    const value = n.attr('value') || '';
    return { value, label: cleaned(own || value).label };
  };

  const selectOptions = (n) => n.options()
    .filter((o) => o.value !== '' || !PLACEHOLDER_OPTION.test(squash(o.label)))
    .map((o) => ({ value: o.value, label: squash(o.label) }));

  const selectValue = (n) => {
    const chosen = n.options().find((o) => o.selected);
    return chosen && chosen.value !== '' && !PLACEHOLDER_OPTION.test(chosen.label) ? chosen.label : '';
  };

  const yesNoGroups = (controls) => {
    const groups = new Map();
    for (const n of controls) {
      const word = n.tag === 'button' ? norm(n.text()) : '';
      if ((word !== 'yes' && word !== 'no') || !n.visible() || isSubmitLike(n)) continue;
      const parent = n.parent();
      if (!groups.has(parent.raw)) groups.set(parent.raw, { parent });
      groups.get(parent.raw)[word] = n;
    }
    return [...groups.values()].filter((g) => g.yes && g.no);
  };

  const radioOrCheckbox = (n, type) => n.tag === 'input' && (type === 'radio' || type === 'checkbox');

  const collect = (doc) => {
    const controls = doc.controls();
    const fields = [];
    const handles = {};
    const consumed = new Set();
    const at = new Map();
    const add = (n, field, handle) => {
      const base = n.attr('name') || n.attr('id') || 'field-' + (fields.length + 1);
      let key = base;
      for (let i = 2; handles[key]; i++) key = base + '#' + i;
      fields.push({ key, ...field });
      at.set(key, controls.indexOf(n));
      handles[key] = handle;
    };
    const readable = (n) => {
      const type = norm(n.attr('type')) || 'text';
      if (consumed.has(n.raw) || n.tag === 'button') return false;
      if (n.tag === 'input' && SKIP_TYPES.has(type)) return false;
      if (n.attr('aria-hidden') === 'true' || isSubmitLike(n)) return false;
      return type === 'file' || n.visible();
    };

    for (const g of yesNoGroups(controls)) {
      const text = cleaned(groupLabel(g.parent));
      const siblings = controls.filter((c) => c.parent().raw === g.parent.raw);
      siblings.forEach((c) => consumed.add(c.raw));
      const hidden = siblings.find((c) => c.tag === 'input');
      const pressed = g.yes.attr('aria-pressed') === 'true' ? 'yes' : g.no.attr('aria-pressed') === 'true' ? 'no' : '';
      add(hidden || g.yes, {
        label: text.label,
        kind: 'yesno',
        required: isRequired(hidden || g.yes, text.required),
        options: [{ value: 'yes', label: 'Yes' }, { value: 'no', label: 'No' }],
        value: pressed,
      }, { kind: 'yesno', nodes: { yes: g.yes, no: g.no } });
    }

    const members = new Map();
    const groupKey = (n, type) => type + ':' + (n.attr('name') || n.attr('id') || 'at-' + controls.indexOf(n));
    for (const n of controls.filter(readable)) {
      const type = norm(n.attr('type')) || 'text';
      if (!radioOrCheckbox(n, type)) continue;
      const gk = groupKey(n, type);
      members.set(gk, (members.get(gk) || []).concat(n));
    }

    for (const n of controls.filter(readable)) {
      const type = norm(n.attr('type')) || 'text';
      if (radioOrCheckbox(n, type)) {
        const gk = groupKey(n, type);
        if (members.has(gk) && members.get(gk)[0] === n) addGroup(add, type === 'radio', members.get(gk));
        continue;
      }
      let kind = 'text';
      let options = [];
      let value = squash(n.value());
      let format = '';
      if (norm(n.attr('role')) === 'combobox') kind = 'combobox';
      else if (n.tag === 'select') {
        kind = 'select';
        options = selectOptions(n);
        value = selectValue(n);
      } else if (n.tag === 'textarea') kind = 'textarea';
      else if (type === 'file') kind = 'file';
      else if (type === 'date') {
        kind = 'date';
        format = 'YYYY-MM-DD';
      } else if (TEXT_TYPES.has(type)) kind = type;
      const text = cleaned(kind === 'file' ? groupLabel(n) || controlLabel(n) : controlLabel(n));
      const placeholder = squash(n.attr('placeholder'));
      if (kind === 'text' && DATE_FORMAT.test(placeholder)) format = placeholder;
      const field = { label: text.label, kind, required: isRequired(n, text.required), options, value };
      if (format) field.format = format;
      add(n, field, { kind, node: n });
    }

    fields.sort((a, b) => at.get(a.key) - at.get(b.key));
    return { fields, handles };
  };

  const addGroup = (add, radio, nodes) => {
    const options = nodes.map(optionOf);
    const single = !radio && nodes.length === 1;
    const text = cleaned(groupLabel(nodes[0]));
    const own = options[0].label;
    const kind = radio ? 'radio' : single ? 'checkbox' : 'checkboxes';
    const labels = nodes.filter((m) => m.checked()).map((m) => options[nodes.indexOf(m)].label);
    const field = {
      label: single && own && !/^(yes|no)$/i.test(own) ? own : text.label || own,
      kind,
      required: isRequired(nodes[0], text.required),
      options,
      value: kind === 'checkboxes' ? '' : labels[0] || '',
    };
    if (kind === 'checkboxes') field.values = labels;
    add(nodes[0], field, { kind, nodes });
  };

  return { collect };
})();

if (typeof module !== 'undefined') module.exports = SiftyCollect;
