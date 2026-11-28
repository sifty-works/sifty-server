var SiftyPanel = (() => {
  const CSS = `
    .box{position:fixed;top:12px;right:12px;width:320px;max-height:80vh;overflow:auto;box-sizing:border-box;padding:12px;background:#fff;color:#1c1b22;font:13px/1.4 system-ui,sans-serif;border:1px solid #cfcde0;border-radius:8px;box-shadow:0 6px 24px rgba(0,0,0,.2)}
    .head{display:flex;justify-content:space-between;font-weight:600;margin-bottom:6px}
    button{all:unset;cursor:pointer;padding:0 4px}
    h4{margin:10px 0 4px;font-size:12px;text-transform:uppercase;color:#5b5873}
    ul{margin:0;padding-left:16px}
    li span{color:#5b5873}
    .mark{position:fixed;padding:1px 5px;border-radius:3px;font:600 11px/1.3 system-ui,sans-serif;color:#fff;pointer-events:none}
    .filled{background:#4b44a8}.left{background:#b36b00}
  `;
  const TONES = { filled: '#4b44a8', left: '#b36b00' };

  const create = (onClose) => {
    const host = document.createElement('div');
    host.style.cssText = 'all:initial;position:fixed;top:0;left:0;z-index:2147483647';
    const root = host.attachShadow({ mode: 'closed' });
    const style = document.createElement('style');
    style.textContent = CSS;
    const box = document.createElement('div');
    box.className = 'box';
    root.append(style, box);
    document.documentElement.appendChild(host);
    const marks = [];
    const outlines = [];

    const el = (tag, text, parent, cls) => {
      const e = document.createElement(tag);
      if (text) e.textContent = text;
      if (cls) e.className = cls;
      if (parent) parent.appendChild(e);
      return e;
    };

    const list = (title, items) => {
      if (!items.length) return;
      el('h4', title, box);
      const ul = el('ul', '', box);
      for (const [label, detail] of items) {
        const li = el('li', label, ul);
        if (detail) el('span', ' — ' + detail, li);
      }
    };

    const place = () => {
      for (const m of marks) {
        const r = m.target.getBoundingClientRect();
        m.node.style.top = Math.max(0, r.top - 8) + 'px';
        m.node.style.left = Math.max(0, r.right - m.node.offsetWidth) + 'px';
        m.node.style.display = r.width + r.height === 0 ? 'none' : '';
      }
    };

    const render = (model) => {
      box.textContent = '';
      const head = el('div', '', box, 'head');
      el('span', 'Sifty', head);
      el('button', '×', head).addEventListener('click', () => close());
      if (model.vacancy) el('div', [model.vacancy.title, model.vacancy.company].filter(Boolean).join(' — '), box);
      if (model.status) el('div', model.status, box);
      list('Filled', model.entries.filter((e) => SiftyFill.DOING.has(e.action)).map((e) => [e.label, e.source]));
      list('For you', model.entries.filter((e) => !SiftyFill.DOING.has(e.action) && e.action !== 'pending').map((e) => [e.label, e.reason]));
      if (model.open) {
        const a = el('a', 'Open in Sifty', el('div', '', box));
        a.href = model.open;
        a.target = '_blank';
        a.rel = 'noopener';
      }
      place();
    };

    const mark = (target, text, tone) => {
      outlines.push([target, target.style.outline]);
      target.style.outline = '2px solid ' + TONES[tone];
      const node = el('span', text, root, 'mark ' + tone);
      marks.push({ target, node });
      place();
    };

    const close = () => {
      window.removeEventListener('scroll', place, true);
      window.removeEventListener('resize', place);
      for (const [t, o] of outlines) t.style.outline = o;
      host.remove();
      onClose();
    };

    window.addEventListener('scroll', place, true);
    window.addEventListener('resize', place);
    return { render, mark, close };
  };

  return { create };
})();
