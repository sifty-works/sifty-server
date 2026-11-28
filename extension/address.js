var SIFTY_DEFAULT_ADDRESS = 'http://127.0.0.1:8080';

var siftyAddress = async () => (await chrome.storage.local.get({ address: SIFTY_DEFAULT_ADDRESS })).address;
