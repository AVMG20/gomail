import { fileURLToPath, URL } from 'node:url'
import { existsSync, mkdirSync, writeFileSync } from 'node:fs'
import { defineConfig, type Plugin, type PluginOption } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import wails from '@wailsio/runtime/plugins/vite'

const bindingsDir = fileURLToPath(new URL('./bindings', import.meta.url))

// The Wails typed-events plugin fails the build when bindings have not been
// generated yet; only enable it once they exist so a plain `npm run build`
// (e.g. CI before `wails3 generate bindings`) still works.
const hasEventBindings = ['js', 'ts'].some((ext) =>
  existsSync(`${bindingsDir}/github.com/wailsapp/wails/v3/internal/eventcreate.${ext}`),
)

// Keep the dist/.gitkeep placeholder so a fresh clone can still compile the
// Go embed of frontend/dist.
function keepGitkeep(): Plugin {
  return {
    name: 'keep-gitkeep',
    apply: 'build',
    closeBundle() {
      const dir = fileURLToPath(new URL('./dist', import.meta.url))
      mkdirSync(dir, { recursive: true })
      writeFileSync(`${dir}/.gitkeep`, '')
    },
  }
}

const plugins: PluginOption[] = [vue(), tailwindcss(), keepGitkeep()]
if (hasEventBindings) plugins.splice(2, 0, wails('./bindings'))

export default defineConfig({
  plugins,
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    host: '127.0.0.1',
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  build: {
    target: ['es2021', 'safari15'],
    chunkSizeWarningLimit: 800,
  },
})
