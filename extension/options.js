const input = document.getElementById('address');
const status = document.getElementById('status');

siftyAddress().then((a) => { input.value = a; });

const valid = (text) => {
  try {
    const u = new URL(text);
    return u.protocol === 'http:' && (u.hostname === '127.0.0.1' || u.hostname === 'localhost') ? u.origin : null;
  } catch {
    return null;
  }
};

document.getElementById('save').addEventListener('click', async () => {
  const address = valid(input.value.trim());
  if (!address) {
    status.textContent = 'Use an address on this computer, such as ' + SIFTY_DEFAULT_ADDRESS;
    return;
  }
  await chrome.storage.local.set({ address });
  input.value = address;
  status.textContent = 'Saved';
});
