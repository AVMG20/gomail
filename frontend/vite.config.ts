import { fileURLToPath, URL } from 'node:url'
import { writeFileSync, mkdirSync } from 'node:fs'
import { defineConfig, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

// Wails embeds frontend/dist; keep the .gitkeep placeholder after a build
// empties the directory so a fresh clone still compiles the Go embed.
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

export default defineConfig({
  plugins: [vue(), tailwindcss(), keepGitkeep()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    target: ['es2021', 'safari15'],
    chunkSizeWarningLimit: 800,
  },
})
