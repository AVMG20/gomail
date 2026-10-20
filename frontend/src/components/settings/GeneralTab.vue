<script setup lang="ts">
import { Switch } from '@/components/ui/switch'
import { Slider } from '@/components/ui/slider'
import { localeUses12h } from '@/lib/calendar'
import { shortcutLabel } from '@/lib/platform'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { useSettingsStore } from '@/stores/settings'
import { useMailStore } from '@/stores/mail'
import { ref } from 'vue'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'
import TrustedLists from './TrustedLists.vue'

const settings = useSettingsStore()
const localeClock = localeUses12h() ? '12-hour' : '24-hour'
const intervals = [1, 5, 15, 30, 60]
const mail = useMailStore()
const showTrusted = ref(false)
const trustedCount = () => (settings.settings?.trustedImageSenders?.length ?? 0) + (settings.settings?.trustedLinkDomains?.length ?? 0)
async function setConversation(v: boolean) {
  await settings.update({ conversationView: v }, 0)
  void mail.reload()
}
</script>

<template>
  <div v-if="settings.settings">
    <SettingSection title="Reading">
      <SettingRow title="Group messages into conversations" description="Show one row per conversation instead of one per message.">
        <Switch :model-value="settings.settings.conversationView ?? true" @update:model-value="(v: boolean) => setConversation(v)" />
      </SettingRow>
      <SettingRow title="Mark as read" description="When an opened message counts as read.">
        <Select :model-value="settings.settings.markReadMode ?? 'immediate'" @update:model-value="(v) => settings.update({ markReadMode: v as 'immediate' | 'delay' | 'manual' })">
          <SelectTrigger class="w-40"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="immediate">Immediately</SelectItem>
            <SelectItem value="delay">After 2 seconds</SelectItem>
            <SelectItem value="manual">Manually</SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>
      <SettingRow title="Time format" :description="`For mail and calendar times. Follows your language and region by default (${localeClock}).`">
        <Select :model-value="settings.settings.calendarTimeFormat || 'auto'" @update:model-value="(v) => settings.update({ calendarTimeFormat: String(v) })">
          <SelectTrigger class="w-44"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="auto">Automatic ({{ localeClock }})</SelectItem>
            <SelectItem value="12h">12-hour (1:30 PM)</SelectItem>
            <SelectItem value="24h">24-hour (13:30)</SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>
      <SettingRow title="Reading pane" description="Off shows a full-width list; messages open in place with a back button.">
        <div class="bg-muted inline-flex rounded-lg p-0.5">
          <button v-for="o in [{ v: 'right', l: 'Right' }, { v: 'off', l: 'Off' }]" :key="o.v" type="button" :class="['rounded-md px-3 py-1 text-xs font-medium transition-colors', (settings.settings.readingPane ?? 'right') === o.v ? 'bg-background shadow-sm' : 'text-muted-foreground hover:text-foreground']" @click="settings.update({ readingPane: o.v as 'right' | 'off' })">{{ o.l }}</button>
        </div>
      </SettingRow>
      <SettingRow title="Text size" :description="`${Math.round((settings.settings.zoom ?? 1) * 100)}% · ${shortcutLabel('mod+=')} / ${shortcutLabel('mod+-')} / ${shortcutLabel('mod+0')}`">
        <div class="flex w-56 items-center gap-3">
          <Slider :model-value="[settings.settings.zoom ?? 1]" :min="0.9" :max="1.25" :step="0.05" class="flex-1" @update:model-value="(v) => v && settings.update({ zoom: Math.round(v[0] * 100) / 100 })" />
          <button type="button" class="text-muted-foreground hover:text-foreground text-xs" @click="settings.update({ zoom: 1 })">Reset</button>
        </div>
      </SettingRow>
    </SettingSection>
    <SettingSection title="Sync">
      <SettingRow title="Check for new mail" description="How often gomail syncs in the background.">
        <Select :model-value="String(settings.settings.syncIntervalMinutes)" @update:model-value="(v) => settings.update({ syncIntervalMinutes: Number(v) })">
          <SelectTrigger class="w-40"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem v-for="i in intervals" :key="i" :value="String(i)">Every {{ i === 1 ? 'minute' : i === 60 ? 'hour' : `${i} minutes` }}</SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>
    </SettingSection>
    <SettingSection title="Storage">
      <SettingRow title="Keep mail offline for" description="How far back gomail downloads mail. Older mail stays on the server and loads on demand.">
        <Select :model-value="String(settings.settings.syncDepthDays ?? 90)" @update:model-value="(v) => settings.update({ syncDepthDays: Number(v) })">
          <SelectTrigger class="w-40"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem v-for="d in [30, 90, 365, 0]" :key="d" :value="String(d)">{{ d === 0 ? 'Everything' : d === 365 ? '1 year' : `${d} days` }}</SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>
      <SettingRow title="Pause background download" description="Stop fetching older mail in the background (new mail still arrives).">
        <Switch :model-value="!!settings.settings.backfillPaused" aria-label="Pause background download" @update:model-value="(v: boolean) => settings.update({ backfillPaused: v })" />
      </SettingRow>
    </SettingSection>
    <SettingSection title="Startup & updates">
      <SettingRow title="Open gomail when you log in" description="Starts in the background so new mail arrives right away.">
        <Switch :model-value="!!settings.settings.launchAtLogin" aria-label="Open gomail when you log in" @update:model-value="(v: boolean) => settings.update({ launchAtLogin: v })" />
      </SettingRow>
      <SettingRow title="Check for updates" description="Looks for a new release on GitHub once a day. Nothing is installed automatically.">
        <Switch :model-value="settings.settings.checkUpdates !== false" aria-label="Check for updates" @update:model-value="(v: boolean) => settings.update({ checkUpdates: v })" />
      </SettingRow>
    </SettingSection>
    <SettingSection title="Notifications">
      <SettingRow title="Desktop notifications for new mail" description="Show a system notification when new mail arrives.">
        <Switch :model-value="settings.settings.notifications" @update:model-value="(v: boolean) => settings.update({ notifications: v })" />
      </SettingRow>
      <SettingRow title="Show in system tray" description="Keep gomail in the tray / menu bar with an unread badge.">
        <Switch :model-value="settings.settings.showInTray ?? true" @update:model-value="(v: boolean) => settings.update({ showInTray: v })" />
      </SettingRow>
      <SettingRow v-if="settings.settings.showInTray ?? true" title="Keep running when the window is closed" description="Closing the window hides gomail to the tray so new mail still arrives.">
        <Switch :model-value="settings.settings.closeToTray ?? false" @update:model-value="(v: boolean) => settings.update({ closeToTray: v })" />
      </SettingRow>
    </SettingSection>
    <SettingSection title="Sending">
      <SettingRow title="Quote the original in replies" description="What a reply or reply-all starts with below your text. Forwards always include the message.">
        <Select :model-value="settings.settings.replyQuote || 'header'" @update:model-value="(v) => settings.update({ replyQuote: v as 'header' | 'plain' | 'none' })">
          <SelectTrigger class="w-48"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="header">With “On … wrote:”</SelectItem>
            <SelectItem value="plain">Quote only</SelectItem>
            <SelectItem value="none">Don’t quote</SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>
      <SettingRow title="Undo send" description="Wait a few seconds before sending so you can take it back.">
        <Select :model-value="String(settings.settings.undoSendSeconds ?? 5)" @update:model-value="(v) => settings.update({ undoSendSeconds: Number(v) })">
          <SelectTrigger class="w-40"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem v-for="n in [0, 5, 10, 20]" :key="n" :value="String(n)">{{ n === 0 ? 'Off' : `${n} seconds` }}</SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>
    </SettingSection>
    <SettingSection title="Privacy">
      <SettingRow title="Load remote images automatically" description="Remote images can reveal when and where you open a message. When off, you can still load them per message.">
        <Switch :model-value="settings.settings.showRemoteImages" @update:model-value="(v: boolean) => settings.update({ showRemoteImages: v })" />
      </SettingRow>
      <div class="text-muted-foreground -mt-1.5 pb-3 text-xs">
        Trusted senders and links<template v-if="trustedCount()"> ({{ trustedCount() }})</template> ·
        <button type="button" class="text-foreground underline-offset-2 hover:underline" :aria-expanded="showTrusted" @click="showTrusted = !showTrusted">{{ showTrusted ? 'Hide' : 'Manage' }}</button>
      </div>
      <TrustedLists v-if="showTrusted" />
    </SettingSection>
  </div>
</template>
