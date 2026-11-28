importScripts('address.js');

const PAGE_SCRIPTS = ['plan.js', 'collect.js', 'dom.js', 'fill.js', 'panel.js', 'content.js'];

chrome.action.onClicked.addListener((tab) => {
  chrome.scripting.executeScript({ target: { tabId: tab.id }, files: PAGE_SCRIPTS });
});

const route = (msg) => {
  if (msg.kind === 'ask') return ['POST', '/api/extension/fill', JSON.stringify(msg.body)];
  if (msg.kind === 'poll' && Number.isInteger(msg.form)) return ['GET', '/api/extension/fill/' + msg.form, undefined];
  return null;
};

const call = async (msg) => {
  const r = route(msg);
  if (!r) return { error: 'refused' };
  const address = await siftyAddress();
  try {
    const res = await fetch(address + r[1], { method: r[0], headers: r[2] ? { 'Content-Type': 'application/json' } : {}, body: r[2] });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) return { error: 'http', status: res.status, message: data.error || '' };
    return { data, address };
  } catch {
    return { error: 'offline' };
  }
};

chrome.runtime.onMessage.addListener((msg, sender, respond) => {
  call(msg).then(respond);
  return true;
});
