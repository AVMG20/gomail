<script setup lang="ts">
// "Rules for <sender>" submenu of the reading pane's ⋯ menu and the list's right-click menu:
// always send the sender to Junk (the address, or everyone at a company domain) or always
// label it. Rules that already match show checked; picking a checked one removes it.
import { computed } from 'vue'
import { PlusIcon, ShieldBanIcon, SlidersHorizontalIcon, TagIcon, WorkflowIcon } from '@lucide/vue'
import {
  ContextMenuCheckboxItem, ContextMenuItem, ContextMenuLabel, ContextMenuSeparator, ContextMenuSub, ContextMenuSubContent, ContextMenuSubTrigger,
} from '@/components/ui/context-menu'
import {
  DropdownMenuCheckboxItem, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuSub, DropdownMenuSubContent, DropdownMenuSubTrigger,
} from '@/components/ui/dropdown-menu'
import { domainOf, junkableDomain, rulesForSender } from '@/lib/junk'
import type { Address } from '@/lib/types'
import { useAccountsStore } from '@/stores/accounts'
import { useSenderRulesStore } from '@/stores/senderRules'
import { useSettingsStore } from '@/stores/settings'
import { useUiStore } from '@/stores/ui'

const props = defineProps<{
  /** Which menu it sits in: the parts differ. */
  menu: 'context' | 'dropdown'
  message: { id: string; accountId: string; from: Address }
}>()

const M = computed(() => props.menu === 'context'
  ? { Sub: ContextMenuSub, Trigger: ContextMenuSubTrigger, Content: ContextMenuSubContent, Item: ContextMenuItem, Check: ContextMenuCheckboxItem, Label: ContextMenuLabel, Sep: ContextMenuSeparator }
  : { Sub: DropdownMenuSub, Trigger: DropdownMenuSubTrigger, Content: DropdownMenuSubContent, Item: DropdownMenuItem, Check: DropdownMenuCheckboxItem, Label: DropdownMenuLabel, Sep: DropdownMenuSeparator })

const rules = useSenderRulesStore()
const settings = useSettingsStore()
const accounts = useAccountsStore()
const ui = useUiStore()

const accountId = computed(() => props.message.accountId)
const address = computed(() => props.message.from.email.trim().toLowerCase())
const ownEmail = computed(() => accounts.accountMap.get(accountId.value)?.email ?? '')
/** Mail the mailbox sent itself has no sender to make rules for. */
const shown = computed(() => rules.available() && !!domainOf(address.value) && address.value !== ownEmail.value.toLowerCase())
const who = computed(() => props.message.from.name?.trim() || address.value)
const domain = computed(() => junkableDomain(address.value, ownEmail.value))

const matching = computed(() => rulesForSender(settings.settings?.labelRules, accountId.value, address.value))
const junkRules = computed(() => matching.value.filter((r) => r.action === 'junk'))
const domainJunk = computed(() => junkRules.value.filter((r) => r.contains.trim().toLowerCase() === `@${domain.value}`))
const labels = computed(() => settings.labelsForAccount(accountId.value).filter((l) => l.id !== 'junk'))
const labelRules = (labelId: string) => matching.value.filter((r) => r.action === 'label' && r.labelId === labelId)

function toggleJunk() {
  if (junkRules.value.length) void rules.removeRules(junkRules.value, `${address.value} no longer goes to Junk`)
  else void rules.junkSender([props.message.id], 'address', { accountId: accountId.value, address: address.value })
}
function toggleDomainJunk() {
  if (domainJunk.value.length) void rules.removeRules(domainJunk.value, `${domain.value} no longer goes to Junk`)
  else void rules.junkSender([props.message.id], 'domain', { accountId: accountId.value, address: address.value })
}
function toggleLabel(labelId: string) {
  const have = labelRules(labelId)
  const name = settings.labelMap.get(labelId)?.name ?? 'the label'
  if (have.length) void rules.removeRules(have, `${address.value} is no longer labeled “${name}”`)
  else void rules.labelSender(accountId.value, address.value, labelId)
}
function editRules() {
  ui.openSettings(`mailbox:${accountId.value}`, 'labels')
}
</script>

<template>
  <component :is="M.Sub" v-if="shown">
    <component :is="M.Trigger" class="gap-2"><WorkflowIcon class="size-4" /> <span class="truncate">Rules for {{ who }}</span></component>
    <component :is="M.Content" class="w-72">
      <component :is="M.Label" class="text-muted-foreground truncate text-xs font-normal">{{ address }}</component>
      <component :is="M.Check" :model-value="junkRules.length > 0" @select="toggleJunk">
        <ShieldBanIcon /> <span class="truncate">Always send to Junk</span>
      </component>
      <component :is="M.Check" v-if="domain" :model-value="domainJunk.length > 0" @select="toggleDomainJunk">
        <ShieldBanIcon /> <span class="truncate">Always send everyone at {{ domain }} to Junk</span>
      </component>
      <component :is="M.Sep" />
      <component :is="M.Sub">
        <component :is="M.Trigger" class="gap-2"><TagIcon class="size-4" /> Always label as</component>
        <component :is="M.Content" class="w-52">
          <component :is="M.Check" v-for="l in labels" :key="l.id" :model-value="labelRules(l.id).length > 0" @select="toggleLabel(l.id)">
            <span class="size-2.5 shrink-0 rounded-full" :style="{ background: l.color }" /> <span class="truncate">{{ l.name }}</span>
          </component>
          <component :is="M.Item" v-if="!labels.length" disabled>No labels in this mailbox</component>
          <component :is="M.Sep" />
          <component :is="M.Item" @select="editRules"><PlusIcon /> New label…</component>
        </component>
      </component>
      <component :is="M.Sep" />
      <component :is="M.Item" @select="editRules"><SlidersHorizontalIcon /> Edit rules…</component>
    </component>
  </component>
</template>
