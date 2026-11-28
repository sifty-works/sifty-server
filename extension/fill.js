var SiftyFill = (() => {
  const { isSubmitLike, norm } = SiftyPlan;
  const DOING = new Set(['fill', 'select', 'check', 'press', 'combo']);
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  const view = (el) => el.ownerDocument.defaultView;

  const setValue = (el, v) => {
    const w = view(el);
    const proto = { SELECT: w.HTMLSelectElement, TEXTAREA: w.HTMLTextAreaElement }[el.tagName] || w.HTMLInputElement;
    Object.getOwnPropertyDescriptor(proto.prototype, 'value').set.call(el, v);
    el.dispatchEvent(new w.Event('input', { bubbles: true }));
    el.dispatchEvent(new w.Event('change', { bubbles: true }));
  };

  const click = (el) => {
    if (isSubmitLike(SiftyDom.wrap(el))) return false;
    const w = view(el);
    for (const type of ['mousedown', 'mouseup']) el.dispatchEvent(new w.MouseEvent(type, { bubbles: true, cancelable: true, view: w }));
    el.click();
    return true;
  };

  const tick = (el) => el.checked || (click(el) && el.checked);

  const visibleOption = (o) => o.getClientRects().length > 0;

  const combo = async (e, h) => {
    const el = h.node.raw;
    const want = norm(e.value);
    if (el.tagName === 'INPUT') {
      el.focus();
      setValue(el, e.value);
    } else if (!click(el)) return false;
    await sleep(300);
    const options = [...el.ownerDocument.querySelectorAll('[role="option"]')].filter(visibleOption);
    const partial = options.filter((o) => want && norm(o.textContent).includes(want));
    const pick = options.find((o) => norm(o.textContent) === want) || (partial.length === 1 ? partial[0] : null);
    if (pick && click(pick)) {
      await sleep(100);
      return true;
    }
    if (el.tagName === 'INPUT') setValue(el, '');
    return false;
  };

  const doers = {
    fill: (e, h) => (setValue(h.node.raw, e.value), true),
    select: (e, h) => {
      if (h.kind === 'select') return setValue(h.node.raw, e.options[0]), true;
      return tick(h.nodes[e.picks[0]].raw);
    },
    check: (e, h) => e.picks.every((i) => tick(h.nodes[i].raw)),
    press: (e, h) => {
      const b = h.nodes[e.option];
      return b.attr('aria-pressed') === 'true' || click(b.raw);
    },
    combo,
  };

  const apply = async (e, h) => {
    let ok = false;
    try {
      ok = await doers[e.action](e, h);
    } catch {
      ok = false;
    }
    return ok ? e : { ...e, action: 'choose', reason: 'Choose: ' + e.value };
  };

  return { apply, DOING };
})();
