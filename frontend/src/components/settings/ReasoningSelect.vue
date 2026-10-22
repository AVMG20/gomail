<script setup lang="ts">
// OpenRouter's reasoning efforts; "default" sends none and leaves it to the model.
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'

defineProps<{ label: string }>()
const effort = defineModel<string>({ required: true })

const EFFORTS = [
  { value: 'default', label: 'Model default' },
  { value: 'none', label: 'Off' },
  { value: 'minimal', label: 'Minimal' },
  { value: 'low', label: 'Low' },
  { value: 'medium', label: 'Medium' },
  { value: 'high', label: 'High' },
  { value: 'xhigh', label: 'Extra high' },
]
</script>

<template>
  <Select v-model="effort">
    <SelectTrigger class="w-40" :aria-label="label"><SelectValue /></SelectTrigger>
    <SelectContent>
      <SelectItem v-for="e in EFFORTS" :key="e.value" :value="e.value">{{ e.label }}</SelectItem>
    </SelectContent>
  </Select>
</template>
