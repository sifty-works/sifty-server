var SiftyDom = (() => {
  const CONTROLS = 'input, textarea, select, button, [role="combobox"]';
  const cache = new WeakMap();
  const SKIPPED = new Set(['SCRIPT', 'STYLE', 'SELECT', 'TEXTAREA']);

  const textOf = (node, skip) => {
    if (node === skip) return '';
    if (node.nodeType === 3) return node.nodeValue;
    if (node.nodeType !== 1 || SKIPPED.has(node.tagName)) return '';
    return [...node.childNodes].map((c) => textOf(c, skip)).join(' ');
  };

  const squash = (s) => String(s || '').replace(/\s+/g, ' ').trim();
  const isControl = (s) => s.matches(CONTROLS) || s.querySelector(CONTROLS);
  const LABELISH = /label|title|question|heading/i;

  const boxVisible = (x) => x.getClientRects().length > 0 && x.ownerDocument.defaultView.getComputedStyle(x).visibility !== 'hidden';

  const comboValue = (el) => {
    let p = el.parentElement;
    for (let i = 0; i < 4 && p; i++, p = p.parentElement) {
      const v = p.querySelector('[class*="single-value"], [class*="singleValue"]');
      if (v) return v.textContent;
    }
    return '';
  };

  const wrap = (el) => {
    if (!el) return null;
    if (!cache.has(el)) {
      cache.set(el, {
        raw: el,
        tag: el.tagName.toLowerCase(),
        attr: (name) => el.getAttribute(name),
        parent: () => wrap(el.parentElement),
        text: () => textOf(el),
        textWithout: (x) => textOf(el, x && x.raw),
        find: (tag) => wrap(el.querySelector(tag)),
        byId: (id) => wrap(el.ownerDocument.getElementById(id)),
        labelsFor: (id) => [...el.ownerDocument.querySelectorAll('label[for]')].filter((l) => l.htmlFor === id).map(wrap),
        visible: () => boxVisible(el) || (!!el.labels && [...el.labels].some(boxVisible)),
        value: () => el.value || (el.getAttribute('role') === 'combobox' ? comboValue(el) : ''),
        checked: () => !!el.checked,
        options: () => [...(el.options || [])].map((o) => ({ value: o.value, label: o.textContent, selected: o.selected })),
        hasForm: () => !!el.form,
        prevText: () => {
          const found = [];
          for (let s = el.previousElementSibling; s && found.length < 3; s = s.previousElementSibling) {
            if (!isControl(s) && squash(s.textContent)) found.push(s);
          }
          const best = found.find((s) => /^(LABEL|LEGEND|H[1-6])$/.test(s.tagName) || LABELISH.test(s.getAttribute('class') || '')) || found[0];
          return best ? textOf(best) : '';
        },
      });
    }
    return cache.get(el);
  };

  const documents = (doc) => [doc].concat([...doc.querySelectorAll('iframe, frame')].flatMap((f) => (f.contentDocument ? documents(f.contentDocument) : [])));

  const page = (doc) => ({ controls: () => documents(doc).flatMap((d) => [...d.querySelectorAll(CONTROLS)]).map(wrap) });

  const mainText = (doc) => ((doc.querySelector('main') || doc.body).innerText || '').slice(0, 20000);

  return { wrap, page, mainText };
})();
