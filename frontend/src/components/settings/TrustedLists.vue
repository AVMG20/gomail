<script setup lang="ts">
// Settings → Mail → Privacy → "Manage": the link domains that open without asking and
// the senders whose mail always loads remote images. Deliberately tucked away.
import { computed } from 'vue'
import { XIcon } from '@lucide/vue'
import { useSettingsStore } from '@/stores/settings'

const settings = useSettingsStore()
const groups = computed(() => [
  {
    key: 'trustedImageSenders' as const,
    title: 'Always load images from',
    empty: 'No senders yet. Use “Always load images from …” above a message.',
    items: settings.settings?.trustedImageSenders ?? [],
  },
  {
    key: 'trustedLinkDomains' as const,
    title: 'Open links without asking',
    empty: 'No sites yet. Use “Always allow …” when opening a link.',
    items: settings.settings?.trustedLinkDomains ?? [],
  },
])

function remove(key: 'trustedImageSenders' | 'trustedLinkDomains', value: string) {
  const list = settings.settings?.[key] ?? []
  void settings.update({ [key]: list.filter((v) => v !== value) }, 0)
}
</script>

<template>
  <div class="space-y-4 pb-3.5">
    <div v-for="g in groups" :key="g.key">
      <div class="text-muted-foreground mb-1.5 text-xs font-medium">{{ g.title }}</div>
      <p v-if="!g.items.length" class="text-muted-foreground text-xs">{{ g.empty }}</p>
      <ul v-else class="border-border divide-border max-h-48 divide-y overflow-y-auto rounded-md border" :aria-label="g.title">
        <li v-for="v in g.items" :key="v" class="flex items-center gap-2 px-3 py-1.5 text-[13px]">
          <span class="selectable min-w-0 flex-1 truncate">{{ v }}</span>
          <button
            type="button"
            class="text-muted-foreground hover:text-foreground hover:bg-muted flex size-6 shrink-0 items-center justify-center rounded"
            :aria-label="`Remove ${v}`"
            :title="`Remove ${v}`"
            @click="remove(g.key, v)"
          >
            <XIcon class="size-3.5" />
          </button>
        </li>
      </ul>
    </div>
  </div>
</template>
