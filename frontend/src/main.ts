import { createApp } from 'vue'
import { createPinia } from 'pinia'
import 'vue-sonner/style.css'
import './style.css'
import App from './App.vue'
import { applyTheme, readCachedTheme } from './lib/theme'
import { initBackend } from './lib/api'
import { installErrorHandlers } from './lib/errors'

// The inline script in index.html already applied the cached theme before
// first paint; re-apply here so the OS-theme listener is registered.
const cached = readCachedTheme()
if (cached) applyTheme(cached, false)

if (import.meta.env.VITE_E2E) void import('./e2eHook') // TEMPORARY

// Load the Wails runtime + generated bindings first (lazy chunk; skipped in the browser mock).
void initBackend()
  .catch((e) => console.error('[gomail] failed to initialise the backend bridge', e))
  .finally(() => {
    const app = createApp(App).use(createPinia())
    installErrorHandlers(app)
    app.mount('#app')
  })
