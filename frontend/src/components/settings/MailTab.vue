<script setup lang="ts">
// Settings → Mail: how mail is read, sent and kept private.
import { ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { useSettingsStore } from '@/stores/settings'
import { useMailStore } from '@/stores/mail'
import { useUiStore } from '@/stores/ui'
import SettingRow from './SettingRow.vue'
import SettingSection from './SettingSection.vue'
import TrustedLists from './TrustedLists.vue'

const settings = useSettingsStore()
const mail = useMailStore()
const ui = useUiStore()
const showTrusted = ref(false)
const trustedCount = () => (settings.settings?.trustedImageSenders?.length ?? 0) + (settings.settings?.trustedLinkDomains?.length ?? 0)
async function setConversation(v: boolean) {
  await settings.update({ conversationView: v }, 0)
  void mail.reload()
}
</script>

<template>
  <div v-if="settings.settings">
    <SettingSection title="Reading" all-mailboxes>
      <SettingRow title="Group messages into conversations" description="One row per conversation instead of one per message.">
        <Switch :model-value="settings.settings.conversationView ?? true" @update:model-value="(v: boolean) => setConversation(v)" />
      </SettingRow>
      <SettingRow title="Mark as read" description="When an opened message counts as read.">
        <Select :model-value="settings.settings.markReadMode ?? 'immediate'" @update:model-value="(v) => settings.update({ markReadMode: v as 'immediate' | 'delay' | 'manual' })">
          <SelectTrigger class="w-44"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="immediate">Immediately</SelectItem>
            <SelectItem value="delay">After 2 seconds</SelectItem>
            <SelectItem value="manual">Manually</SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>
    </SettingSection>

    <SettingSection title="Labels" all-mailboxes>
      <SettingRow title="Move mail when you add a label yourself" description="Jev and rules move mail by each label’s choice (Archive, Junk, Trash). Turn on to do the same when you add a label yourself.">
        <Switch :model-value="settings.settings.manualLabelActions ?? false" @update:model-value="(v: boolean) => settings.update({ manualLabelActions: v }, 0)" />
      </SettingRow>
    </SettingSection>

    <SettingSection title="Writing" all-mailboxes>
      <SettingRow title="Undo send" description="Waits a few seconds before sending, so you can take it back.">
        <Select :model-value="String(settings.settings.undoSendSeconds ?? 5)" @update:model-value="(v) => settings.update({ undoSendSeconds: Number(v) })">
          <SelectTrigger class="w-44"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem v-for="n in [0, 5, 10, 20]" :key="n" :value="String(n)">{{ n === 0 ? 'Off' : `${n} seconds` }}</SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>
      <SettingRow title="Quote the original in replies" description="What appears below your reply. Forwards always include the message.">
        <Select :model-value="settings.settings.replyQuote || 'header'" @update:model-value="(v) => settings.update({ replyQuote: v as 'header' | 'plain' | 'none' })">
          <SelectTrigger class="w-44"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="header">With “On … wrote:”</SelectItem>
            <SelectItem value="plain">Quote only</SelectItem>
            <SelectItem value="none">Don’t quote</SelectItem>
          </SelectContent>
        </Select>
      </SettingRow>
      <SettingRow title="Signatures" description="Set per mailbox, on its page.">
        <Button size="sm" variant="outline" @click="ui.settingsTab = 'accounts'">Mailboxes</Button>
      </SettingRow>
    </SettingSection>

    <SettingSection title="Privacy" all-mailboxes>
      <SettingRow title="Load remote images automatically" description="Images from the web can tell the sender when you opened a message. When off, you can load them per message.">
        <Switch :model-value="settings.settings.showRemoteImages" @update:model-value="(v: boolean) => settings.update({ showRemoteImages: v })" />
      </SettingRow>
      <SettingRow title="Trusted senders and sites" description="Senders whose images always load, and sites whose links open without asking.">
        <Button size="sm" variant="outline" :aria-expanded="showTrusted" :aria-label="`${showTrusted ? 'Hide' : 'Manage'} trusted senders and sites`" @click="showTrusted = !showTrusted">
          {{ showTrusted ? 'Hide' : 'Manage' }}<template v-if="!showTrusted && trustedCount()"> ({{ trustedCount() }})</template>
        </Button>
      </SettingRow>
      <TrustedLists v-if="showTrusted" class="pt-3.5" />
    </SettingSection>
  </div>
</template>
