<script setup lang="ts">
import { SparklesIcon, TagIcon, ZapIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import { useUiStore } from '@/stores/ui'

const ui = useUiStore()
</script>

<template>
  <div class="bg-background flex h-full flex-1 items-center justify-center p-8">
    <div class="w-full max-w-md text-center">
      <img src="/appicon.svg" alt="" class="mx-auto mb-6 size-20" width="80" height="80" />
      <h1 class="text-2xl font-semibold tracking-tight">Welcome to gomail</h1>
      <p class="text-muted-foreground mt-2 text-sm leading-relaxed">A calm, fast home for all your mail. Connect an account to get started.</p>
      <div class="mt-8 grid gap-2.5">
        <Button size="lg" variant="outline" class="h-12 justify-start gap-3 px-4" @click="ui.addAccountOpen = true">
          <ProviderIcon provider="gmail" class="size-5" /> Connect Gmail
        </Button>
        <Button size="lg" variant="outline" class="h-12 justify-start gap-3 px-4" @click="ui.addAccountOpen = true">
          <ProviderIcon provider="outlook" class="size-5" /> Connect Outlook
        </Button>
      </div>
      <ul class="text-muted-foreground mt-10 grid gap-3 text-left text-xs">
        <li class="flex items-center gap-2.5"><ZapIcon class="size-4" /> Keyboard-first: press <kbd class="border-border rounded border px-1">?</kbd> any time for shortcuts.</li>
        <li class="flex items-center gap-2.5"><TagIcon class="size-4" /> Organise with your own labels, across accounts.</li>
        <li class="flex items-center gap-2.5"><SparklesIcon class="size-4" /> Optional AI labeling and grammar help via OpenRouter.</li>
      </ul>
    </div>
  </div>
</template>
