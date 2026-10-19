import { createApp } from 'vue'
import { createPinia } from 'pinia'
import 'vue-sonner/style.css'
import './style.css'
import App from './App.vue'
import { applyTheme, readCachedTheme } from './lib/theme'

// The inline script in index.html already applied the cached theme before
// first paint; re-apply here so the OS-theme listener is registered.
const cached = readCachedTheme()
if (cached) applyTheme(cached, false)

createApp(App).use(createPinia()).mount('#app')
