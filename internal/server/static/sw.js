// Minimal service worker: it only makes the remote installable ("Add to Home
// Screen"). Nothing is cached, because the page is useless unless the app
// answers.
self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()));
self.addEventListener('fetch', () => {});
