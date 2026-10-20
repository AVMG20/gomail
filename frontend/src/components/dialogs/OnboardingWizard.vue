<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { CheckIcon, ExternalLinkIcon, KeyRoundIcon, LoaderIcon, SparklesIcon, TagIcon } from '@lucide/vue'
import { toast } from 'vue-sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import AccountAvatar from '@/components/common/AccountAvatar.vue'
import { api, errorMessage, writeLocal } from '@/lib/api'
import type { Label } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'

/** embedded = no accounts yet (fills the main area); otherwise a first-run dialog. */
const props = defineProps<{ embedded?: boolean }>()
const emit = defineEmits<{ done: [] }>()
const accounts = useAccountsStore()
const settings = useSettingsStore()
const ui = useUiStore()

const DOCS = 'https://github.com/AVMG20/gomail/blob/main/docs/BACKEND.md'
const step = ref(0)
const open = ref(true)
const steps = ['Connect', 'AI labeling', 'Labels']

// In demo mode the "configure OAuth / try demo mode" hint makes no sense.
const oauthMissing = computed(() => !settings.settings?.googleClientId && !settings.settings?.microsoftClientId && !settings.appInfo?.demo)
onMounted(() => {
  if (!settings.appInfo) void settings.loadAppInfo()
})

// ---- step 2: OpenRouter
const key = ref('')
const savingKey = ref(false)
async function saveKey() {
  savingKey.value = true
  const hadKey = !!settings.settings?.openRouterKeySet
  let saved = false
  try {
    await settings.setOpenRouterKey(key.value.trim())
    saved = true
    await api.testOpenRouter()
    key.value = ''
    toast.success('OpenRouter connected')
  } catch (e) {
    // A key that doesn't work isn't kept (it would look "connected" everywhere).
    if (saved && !hadKey) await settings.setOpenRouterKey('').catch(() => {})
    toast.error('That key didn’t work', { description: errorMessage(e) })
  } finally {
    savingKey.value = false
  }
}

// ---- step 3: starter labels
interface Tpl { name: string; description: string; color: string; on: boolean }
const existing = computed(() => new Set(settings.labels.map((l) => l.name.trim().toLowerCase())))
// Suggestions on top of the labels gomail starts with (Important, Personal, Work, …): only
// ones the user doesn't have yet are offered.
const allTemplates: Tpl[] = [
  { name: 'Travel', description: 'Flights, hotels, bookings and itineraries.', color: '#f97316', on: true },
  { name: 'Receipts', description: 'Order confirmations, receipts and shipping updates.', color: '#14b8a6', on: true },
  { name: 'Family', description: 'Family members, school and household matters.', color: '#f43f5e', on: false },
  { name: 'Shopping', description: 'Online orders, deliveries and returns.', color: '#a855f7', on: false },
  { name: 'Events', description: 'Invitations, tickets and event reminders.', color: '#0ea5e9', on: false },
  { name: 'Work', description: 'Colleagues, projects, meetings and anything job related.', color: '#3b82f6', on: true },
  { name: 'Personal', description: 'Friends and family, personal conversations and invites.', color: '#ec4899', on: true },
  { name: 'Finance', description: 'Invoices, bank statements, payments and taxes.', color: '#22c55e', on: true },
]
const templates = ref<(Tpl & { key: string })[]>(allTemplates.map((t) => ({ ...t, key: t.name.toLowerCase() })))
const offered = computed(() => templates.value.filter((t) => !existing.value.has(t.key)))
const existingNames = computed(() => settings.labels.filter((l) => !l.source || l.source === 'user').map((l) => l.name))

function finish() {
  const picked = offered.value.filter((t) => t.on && t.name.trim() && !existing.value.has(t.name.trim().toLowerCase()))
  if (picked.length && settings.settings) {
    const add: Label[] = picked.map((t, i) => ({ id: `lbl_${Date.now().toString(36)}${i}`, name: t.name.trim(), description: t.description, color: t.color, source: 'user' }))
    void settings.update({ labels: [...settings.labels, ...add] }, 0)
    toast.success(`Added ${add.length} label${add.length === 1 ? '' : 's'}`)
  }
  close()
}

function close() {
  writeLocal('onboarded', true)
  open.value = false
  emit('done')
}

function openDocs(anchor = '') {
  void api.openExternal(`${DOCS}${anchor}`)
}
</script>

<template>
  <component :is="embedded ? 'div' : Dialog" v-bind="embedded ? { class: 'bg-background flex h-full flex-1 items-center justify-center overflow-y-auto p-8' } : { open, 'onUpdate:open': (v: boolean) => !v && close() }">
    <!-- A stray click outside doesn't end the guide for good; Skip setup / Esc do. -->
    <component :is="embedded ? 'div' : DialogContent" :class="embedded ? 'w-full max-w-lg' : 'gap-0 p-0 sm:max-w-[560px]'" v-bind="embedded ? {} : { onInteractOutside: (e: Event) => e.preventDefault() }">
      <div :class="embedded ? '' : 'p-6 pb-4'">
        <div class="mb-5 flex items-center gap-3">
          <img src="/appicon.svg" alt="" width="40" height="40" class="size-10" />
          <div>
            <component :is="embedded ? 'h1' : DialogTitle" class="text-lg font-semibold tracking-tight">Welcome to gomail</component>
            <component :is="embedded ? 'p' : DialogDescription" class="text-muted-foreground text-sm">Three quick steps. You can change everything later in Settings.</component>
          </div>
        </div>

        <!-- Step indicator -->
        <ol class="mb-6 flex items-center gap-2" aria-label="Setup progress">
          <li v-for="(s, i) in steps" :key="s" class="flex flex-1 items-center gap-2">
            <span
              :class="['flex size-6 shrink-0 items-center justify-center rounded-full text-xs font-semibold', i < step ? 'bg-primary text-primary-foreground' : i === step ? 'border-primary text-primary border-2' : 'bg-muted text-muted-foreground']"
              :aria-current="i === step ? 'step' : undefined"
            >
              <CheckIcon v-if="i < step" class="size-3.5" /><template v-else>{{ i + 1 }}</template>
            </span>
            <span :class="['truncate text-xs', i === step ? 'text-foreground font-medium' : 'text-muted-foreground']">{{ s }}</span>
            <span v-if="i < steps.length - 1" class="bg-border h-px flex-1" />
          </li>
        </ol>

        <!-- 1. Connect -->
        <section v-if="step === 0" class="space-y-3">
          <h2 class="text-sm font-semibold">Connect a mailbox</h2>
          <div v-if="accounts.accounts.length" class="flex flex-wrap gap-2">
            <span v-for="a in accounts.accounts" :key="a.id" class="bg-muted inline-flex items-center gap-2 rounded-full py-1 pr-3 pl-1 text-xs">
              <AccountAvatar :account="a" :size="20" :glyph="false" /> {{ a.email }} <CheckIcon class="size-3.5 text-emerald-500" />
            </span>
          </div>
          <div class="grid gap-2 sm:grid-cols-2">
            <Button variant="outline" class="h-11 justify-start gap-3" @click="ui.addAccountOpen = true"><ProviderIcon provider="gmail" class="size-5" /> Connect Gmail</Button>
            <Button variant="outline" class="h-11 justify-start gap-3" @click="ui.addAccountOpen = true"><ProviderIcon provider="outlook" class="size-5" /> Connect Outlook</Button>
          </div>
          <div v-if="oauthMissing" class="bg-muted/60 text-muted-foreground rounded-lg p-3 text-xs leading-relaxed">
            This build needs a one-time sign-in setup before it can connect Gmail or Outlook (official release builds include it).
            <button type="button" class="text-foreground inline-flex items-center gap-1 font-medium hover:underline" @click="openDocs()">Open setup guide <ExternalLinkIcon class="size-3" /></button>
            ·
            <button type="button" class="text-foreground inline-flex items-center gap-1 font-medium hover:underline" @click="ui.openSettings('accounts')">Settings → Accounts</button>
          </div>
        </section>

        <!-- 2. AI -->
        <section v-else-if="step === 1" class="space-y-3">
          <h2 class="flex items-center gap-2 text-sm font-semibold"><SparklesIcon class="size-4 text-violet-500" /> AI labeling and grammar help <span class="text-muted-foreground text-xs font-normal">(optional)</span></h2>
          <p class="text-muted-foreground text-sm leading-relaxed">
            With an OpenRouter key, gomail can sort mail into your labels with the Jev decision model and fix grammar while you write. Mail is only sent to the model when it’s labeled or improved.
          </p>
          <div v-if="settings.aiReady" class="flex items-center gap-2 text-sm text-emerald-700 dark:text-emerald-400"><CheckIcon class="size-4" /> OpenRouter is connected.</div>
          <form v-else class="flex items-center gap-2" @submit.prevent="key.trim() && saveKey()">
            <div class="relative flex-1">
              <KeyRoundIcon class="text-muted-foreground absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2" />
              <Input v-model="key" type="password" autocomplete="off" placeholder="sk-or-v1-…" class="pl-8 text-xs" aria-label="OpenRouter API key" />
            </div>
            <Button type="submit" size="sm" :disabled="!key.trim() || savingKey"><LoaderIcon v-if="savingKey" class="animate-spin" /> Connect</Button>
          </form>
          <button type="button" class="text-muted-foreground hover:text-foreground inline-flex items-center gap-1 text-xs" @click="openDocs('#openrouter')">How to get a key <ExternalLinkIcon class="size-3" /></button>
        </section>

        <!-- 3. Labels -->
        <section v-else class="space-y-3">
          <h2 class="flex items-center gap-2 text-sm font-semibold"><TagIcon class="size-4" /> Add more labels</h2>
          <p class="text-muted-foreground text-xs">
            <template v-if="existingNames.length">You already have {{ existingNames.join(', ') }}. </template>Pick any extras; you can rename them now or later.
          </p>
          <ul class="thin-scroll grid max-h-64 gap-1.5 overflow-y-auto pr-1">
            <li v-for="t in offered" :key="t.key" :class="['flex items-center gap-2.5 rounded-lg border px-2.5 py-1.5 transition-colors', existing.has(t.name.trim().toLowerCase()) ? 'border-border opacity-60' : t.on ? 'border-primary/40 bg-primary/5' : 'border-border']">
              <button type="button" role="checkbox" :disabled="existing.has(t.name.trim().toLowerCase())" :aria-checked="t.on && !existing.has(t.name.trim().toLowerCase())" :aria-label="`Use label ${t.name}`" :class="['flex size-4 shrink-0 items-center justify-center rounded border', t.on && !existing.has(t.name.trim().toLowerCase()) ? 'border-transparent text-white' : 'border-input']" :style="t.on && !existing.has(t.name.trim().toLowerCase()) ? { background: t.color } : {}" @click="t.on = !t.on">
                <CheckIcon v-if="t.on && !existing.has(t.name.trim().toLowerCase())" class="size-3" />
              </button>
              <span class="size-2.5 shrink-0 rounded-full" :style="{ background: t.color }" />
              <input v-model="t.name" class="min-w-0 flex-1 bg-transparent text-sm font-medium outline-none" :aria-label="`Label name (${t.name})`" />
              <span v-if="existing.has(t.name.trim().toLowerCase())" class="text-muted-foreground text-[11px]">already added</span>
            </li>
          </ul>
        </section>
      </div>

      <div :class="['flex items-center gap-2', embedded ? 'mt-8' : 'border-border border-t px-6 py-3']">
        <Button variant="ghost" size="sm" class="text-muted-foreground" @click="close">Skip setup</Button>
        <div class="flex-1" />
        <Button v-if="step > 0" variant="ghost" size="sm" @click="step--">Back</Button>
        <Button v-if="step < steps.length - 1" size="sm" @click="step++">Next</Button>
        <Button v-else size="sm" @click="finish">Finish</Button>
      </div>
    </component>
  </component>
</template>
