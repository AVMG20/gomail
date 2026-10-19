<script setup lang="ts">
defineProps<{ provider: string; class?: string }>()
</script>

<template>
  <!-- Simple generic glyphs: an envelope for Gmail-style, a squared "O" for Outlook-style accounts. -->
  <svg v-if="provider === 'gmail'" viewBox="0 0 24 24" :class="$props.class" aria-label="Gmail">
    <path fill="#4285F4" d="M2 6.5V18a1.5 1.5 0 0 0 1.5 1.5H6V10l-4-3.5Z" />
    <path fill="#34A853" d="M18 19.5h2.5A1.5 1.5 0 0 0 22 18V6.5L18 10v9.5Z" />
    <path fill="#FBBC04" d="M18 5.2V10l4-3.5V5.4c0-1.6-1.9-2.6-3.2-1.6L18 5.2Z" />
    <path fill="#EA4335" d="M6 10V5.2l6 4.6 6-4.6V10l-6 4.6L6 10Z" />
    <path fill="#C5221F" d="M2 5.4v1.1L6 10V5.2L5.2 3.8C3.9 2.8 2 3.8 2 5.4Z" />
  </svg>
  <svg v-else-if="provider === 'outlook'" viewBox="0 0 24 24" :class="$props.class" aria-label="Outlook">
    <rect x="2" y="4" width="20" height="16" rx="3" fill="#0A64C8" />
    <circle cx="9" cy="12" r="4" fill="none" stroke="#fff" stroke-width="2" />
    <path d="M15 9h4M15 12h4M15 15h4" stroke="#9cc6f5" stroke-width="1.6" stroke-linecap="round" />
  </svg>
  <svg v-else viewBox="0 0 24 24" :class="$props.class"><rect x="3" y="5" width="18" height="14" rx="2" fill="currentColor" opacity=".5" /></svg>
</template>
