<script setup lang="ts">
defineProps<{
  colorClass: string
  tooltip?: string
  current: string | number
  max: string | number
  suffix?: string
}>()
</script>

<template>
  <span
    :class="[
      'inline-flex max-w-full flex-wrap items-center gap-x-1 gap-y-0 rounded-md px-1.5 py-px text-[10px] font-medium leading-tight',
      colorClass
    ]"
    :title="tooltip"
  >
    <slot />
    <span class="inline-flex min-w-0 items-center gap-1 whitespace-nowrap">
      <span class="font-mono">{{ current }}</span>
      <span class="text-gray-400 dark:text-gray-500">/</span>
      <span class="font-mono">{{ max }}</span>
    </span>
    <span
      v-if="suffix"
      class="min-w-0 basis-full break-all pl-3 font-mono text-[9px] opacity-60"
    >
      {{ suffix }}
    </span>
  </span>
</template>
