// Minimal service worker: makes the app installable and delivers Web Push
// notifications. Deliberately does not cache API responses (/api/**) - the
// app is a live log viewer, stale data would be actively misleading.
const CACHE_NAME = 'syslytics-shell-v1'
const SHELL_ASSETS = ['/', '/manifest.webmanifest']

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME)
      // Pre-caching the shell is a nice-to-have for offline/fast reloads,
      // not a prerequisite for the worker to run - if a flaky connection
      // (mobile networks, self-signed cert hiccups) makes one of these
      // fetches fail, swallow it rather than leaving install() rejected,
      // which would strand the worker in "installing" forever and block
      // everything that waits on navigator.serviceWorker.ready (push
      // subscribe included).
      .then((cache) => cache.addAll(SHELL_ASSETS).catch(() => {}))
      .then(() => self.skipWaiting())
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE_NAME).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  )
})

self.addEventListener('fetch', (event) => {
  const { request } = event
  if (request.method !== 'GET') return
  const url = new URL(request.url)
  if (url.origin !== self.location.origin || url.pathname.startsWith('/api/')) return

  // Network-first for navigations so users always get the latest build;
  // fall back to the cached shell only when actually offline.
  if (request.mode === 'navigate') {
    event.respondWith(
      fetch(request).catch(() => caches.match('/'))
    )
    return
  }
})

self.addEventListener('push', (event) => {
  let payload = { title: 'Syslytics', body: 'You have a new notification.' }
  if (event.data) {
    try {
      payload = { ...payload, ...event.data.json() }
    } catch {
      payload.body = event.data.text()
    }
  }

  // Each alert is its own event, not an update to the previous one - a
  // shared/fixed tag would make showNotification() silently replace the
  // still-visible prior notification instead of popping a new banner
  // (browsers treat same-tag calls as an in-place update unless renotify
  // is set), which looked like push "stopped working" after the first one.
  const options = {
    body: payload.body || payload.message || '',
    icon: '/icons/icon-192.png',
    badge: '/icons/icon-192.png',
    tag: payload.tag || `syslytics-alert-${Date.now()}`,
    renotify: !!payload.tag,
    data: { url: payload.url || payload.link || '/' },
  }

  event.waitUntil(self.registration.showNotification(payload.title || 'Syslytics', options))
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const targetUrl = event.notification.data?.url || '/'

  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((clients) => {
      for (const client of clients) {
        const clientUrl = new URL(client.url)
        if (clientUrl.origin === self.location.origin && 'focus' in client) {
          client.navigate(targetUrl)
          return client.focus()
        }
      }
      if (self.clients.openWindow) return self.clients.openWindow(targetUrl)
    })
  )
})
