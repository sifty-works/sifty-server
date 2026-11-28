(async () => {
  if (window.siftyPanel) window.siftyPanel.close();
  let closed = false;
  const panel = SiftyPanel.create(() => { closed = true; });
  window.siftyPanel = panel;
  const model = { status: 'Reading the form…', entries: [], vacancy: null, open: '' };
  const show = () => panel.render(model);
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  show();

  const { fields, handles } = SiftyCollect.collect(SiftyDom.page(document));
  if (!fields.length) {
    model.status = 'No form found on this page';
    show();
    return;
  }

  const done = new Map();
  const marked = new Set();

  const target = (key) => {
    const h = handles[key];
    const first = (h.node || h.nodes[0] || h.nodes.yes).raw;
    const group = h.nodes ? first.closest('fieldset, [role="radiogroup"], [role="group"]') || first.parentElement : first;
    for (let p = group; p; p = p.parentElement) if (p.offsetWidth > 8 && p.offsetHeight > 8) return p;
    return first;
  };

  const markOnce = (e, text, tone) => {
    if (marked.has(e.key)) return;
    marked.add(e.key);
    panel.mark(target(e.key), text, tone);
  };

  const absorb = async (data) => {
    const entries = [];
    for (const e of SiftyPlan.plan(fields, data)) {
      if (done.has(e.key)) entries.push(done.get(e.key));
      else if (SiftyFill.DOING.has(e.action)) {
        const r = await SiftyFill.apply(e, handles[e.key]);
        done.set(e.key, r);
        if (r.action !== 'choose') markOnce(r, 'Sifty', 'filled');
        entries.push(r);
      } else {
        if (e.action === 'attach' || e.action === 'consent') markOnce(e, e.reason, 'left');
        entries.push(e);
      }
    }
    model.entries = entries;
  };

  const failure = (res) => {
    if (res.error === 'offline') return 'Sifty is not running on this computer';
    if (res.error === 'http') return res.message || 'Sifty answered with an error (' + res.status + ')';
    return 'Sifty could not be reached';
  };

  const ask = async (msg) => (await chrome.runtime.sendMessage(msg).catch(() => null)) || { error: 'unreachable' };
  const started = Date.now();
  let res = await ask({ kind: 'ask', body: { url: location.href, title: document.title, text: SiftyDom.mainText(document), fields } });

  while (!closed) {
    if (res.error) {
      model.status = failure(res);
      show();
      return;
    }
    const data = res.data;
    model.vacancy = data.vacancy;
    model.open = data.open ? new URL(data.open, res.address).href : '';
    await absorb(data);
    const pending = model.entries.filter((e) => e.action === 'pending').length;
    if (data.state !== 'drafting') {
      model.entries = model.entries.map((e) => (e.action === 'pending' ? { ...e, action: 'left', reason: 'No answer' } : e));
      model.status = '';
      show();
      return;
    }
    if (Date.now() - started > 300000) {
      model.status = 'Sifty did not finish the answers';
      show();
      return;
    }
    model.status = pending ? 'Drafting ' + pending + ' answers…' : 'Drafting answers…';
    show();
    await sleep(2000);
    if (closed) return;
    res = await ask({ kind: 'poll', form: data.form });
  }
})();
