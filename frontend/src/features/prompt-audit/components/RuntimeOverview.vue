<template>
  <section aria-labelledby="prompt-runtime-title" class="space-y-6" data-test="prompt-runtime-overview">
    <h2 id="prompt-runtime-title" class="sr-only">{{ t('admin.promptAudit.runtime.title') }}</h2>

    <div v-if="error" role="alert" class="card">
      <div class="flex flex-col gap-3 p-5 sm:flex-row sm:items-center sm:justify-between">
        <p class="text-sm text-red-700 dark:text-red-300">{{ error }}</p>
        <button type="button" class="btn btn-secondary btn-sm inline-flex items-center gap-2" :disabled="loading" @click="$emit('refresh')">
          <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
          {{ t('admin.promptAudit.actions.retry') }}
        </button>
      </div>
    </div>

    <template v-else-if="loading && !runtime">
      <div class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4" aria-busy="true">
        <div v-for="index in 4" :key="index" class="h-[74px] animate-pulse rounded-lg bg-gray-100 dark:bg-dark-800" />
      </div>
      <div class="grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,520px)_minmax(0,1fr)]">
        <div class="h-80 animate-pulse rounded-lg bg-gray-100 dark:bg-dark-800" />
        <div class="h-80 animate-pulse rounded-lg bg-gray-100 dark:bg-dark-800" />
      </div>
    </template>

    <template v-else-if="runtime">
      <div class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4" data-test="prompt-runtime-overview-cards">
        <div
          v-for="item in overviewItems"
          :key="item.key"
          class="rounded-lg border border-gray-100 bg-white px-4 py-3 shadow-sm dark:border-dark-700 dark:bg-dark-800"
        >
          <div class="flex min-w-0 items-center gap-3">
            <div class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg" :class="item.iconClass">
              <Icon :name="item.icon" size="sm" />
            </div>
            <div class="min-w-0 flex-1">
              <div class="flex min-w-0 items-center justify-between gap-2">
                <p class="truncate text-xs font-medium text-gray-500 dark:text-gray-400">{{ item.label }}</p>
                <span
                  v-if="item.badge"
                  class="inline-flex flex-shrink-0 items-center rounded-full px-2 py-0.5 text-xs font-medium"
                  :class="item.badgeClass"
                >
                  {{ item.badge }}
                </span>
              </div>
              <div class="mt-1 flex min-w-0 items-baseline gap-2">
                <p class="flex-shrink-0 whitespace-nowrap text-xl font-semibold leading-7 text-gray-900 dark:text-white">{{ item.value }}</p>
                <p v-if="item.meta" class="min-w-0 truncate text-xs text-gray-500 dark:text-gray-400">{{ item.meta }}</p>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div class="grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,520px)_minmax(0,1fr)]" data-test="prompt-runtime-detail-cards">
        <div class="card" data-test="prompt-worker-runtime-card">
          <div class="flex flex-col gap-4 border-b border-gray-100 px-6 py-4 dark:border-dark-700 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h3 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.promptAudit.runtime.workerStatus') }}</h3>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.runtime.workerStatusHint') }}</p>
            </div>
            <span class="inline-flex w-fit items-center rounded-full bg-gray-100 px-2.5 py-1 text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300">
              {{ t(`admin.promptAudit.mode.${runtime.effective_mode}`) }}
            </span>
          </div>

          <div class="space-y-5 p-6">
            <div>
              <div class="flex items-center justify-between gap-3">
                <div>
                  <p class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.promptAudit.runtime.queueUsage') }}</p>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    {{ formatNumber(runtime.queue.active) }} / {{ formatNumber(runtime.queue_capacity) }}
                  </p>
                </div>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{ queueUsagePercent }}%</span>
              </div>
              <div class="mt-3 h-2 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700">
                <div class="h-full rounded-full bg-primary-500 transition-all duration-300" :style="{ width: `${queueUsagePercent}%` }" />
              </div>
            </div>

            <div class="grid grid-cols-2 gap-3 sm:grid-cols-3" data-test="prompt-worker-metrics">
              <div v-for="item in workerMetricItems" :key="item.key" class="rounded-lg p-3" :class="item.class">
                <p class="text-xs text-gray-500 dark:text-gray-400">{{ item.label }}</p>
                <p class="mt-2 truncate text-xl font-semibold tabular-nums" :class="item.valueClass">{{ item.value }}</p>
              </div>
            </div>

            <p class="text-xs leading-5 text-gray-500 dark:text-gray-400">
              {{ t('admin.promptAudit.runtime.deliveryTotals', {
                enqueued: formatNumber(runtime.enqueued_total),
                dropped: formatNumber(runtime.dropped_total),
                processed: formatNumber(runtime.processed_total),
                failed: formatNumber(runtime.failed_total),
              }) }}
            </p>
          </div>
        </div>

        <div class="card" data-test="prompt-api-key-load-card">
          <div class="flex flex-col gap-4 border-b border-gray-100 px-6 py-4 dark:border-dark-700 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h3 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.promptAudit.runtime.apiKeyLoad') }}</h3>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.runtime.apiKeyLoadHint') }}</p>
            </div>
            <span class="inline-flex w-fit items-center rounded-full bg-gray-100 px-2.5 py-1 text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300">
              {{ endpointLoadSummary }}
            </span>
          </div>

          <div class="p-6">
            <div v-if="endpointLoads.length > 0" class="max-h-[320px] space-y-3 overflow-y-auto pr-1" data-test="prompt-api-key-load-list">
              <div
                v-for="item in endpointLoads"
                :key="`${item.endpoint_id}:${item.index}`"
                class="rounded-lg bg-gray-50 p-3 dark:bg-dark-700/50"
                data-test="prompt-api-key-load-row"
              >
                <div class="flex min-w-0 flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                  <div class="min-w-0">
                    <div class="flex min-w-0 items-center gap-2">
                      <span class="truncate text-sm font-semibold text-gray-900 dark:text-white">{{ item.endpoint_name || item.endpoint_id }}</span>
                      <span class="h-2 w-2 flex-shrink-0 rounded-full" :class="endpointStatusDotClass(item.status)" />
                      <span class="flex-shrink-0 text-xs text-gray-500 dark:text-gray-400">{{ endpointStatusLabel(item.status) }}</span>
                    </div>
                    <p class="mt-1 truncate font-mono text-xs text-gray-600 dark:text-gray-300">
                      {{ item.key_configured ? item.masked_key : t('admin.promptAudit.runtime.noAPIKey') }}
                    </p>
                    <p class="mt-1 truncate text-xs text-gray-500 dark:text-gray-400">
                      {{ protocolLabel(item.protocol) }} · {{ item.model || '-' }}
                    </p>
                  </div>
                  <div class="grid grid-cols-4 gap-2 text-right text-xs text-gray-500 dark:text-gray-400 sm:min-w-[300px]">
                    <div>
                      <p>{{ t('admin.promptAudit.runtime.keyActiveShort') }}</p>
                      <p class="mt-1 text-sm font-semibold text-sky-700 dark:text-sky-300">{{ formatNumber(item.active) }}</p>
                    </div>
                    <div>
                      <p>{{ t('admin.promptAudit.runtime.keyTotalShort') }}</p>
                      <p class="mt-1 text-sm font-semibold text-gray-900 dark:text-white">{{ formatNumber(item.total) }}</p>
                    </div>
                    <div>
                      <p>{{ t('admin.promptAudit.runtime.keyAvgShort') }}</p>
                      <p class="mt-1 whitespace-nowrap text-sm font-semibold text-gray-900 dark:text-white">{{ formatNumber(item.avg_latency_ms) }} ms</p>
                    </div>
                    <div>
                      <p>{{ t('admin.promptAudit.runtime.keyLastShort') }}</p>
                      <p class="mt-1 whitespace-nowrap text-sm font-semibold text-gray-900 dark:text-white">{{ formatNumber(item.last_latency_ms) }} ms</p>
                    </div>
                  </div>
                </div>
                <div class="mt-3 flex flex-wrap items-center justify-between gap-2 text-xs text-gray-500 dark:text-gray-400">
                  <span>{{ t('admin.promptAudit.runtime.keyTotals', { success: formatNumber(item.success), errors: formatNumber(item.errors) }) }}</span>
                  <span v-if="item.last_http_status || item.last_error_code">
                    <template v-if="item.last_http_status">HTTP {{ item.last_http_status }}</template>
                    <template v-if="item.last_http_status && item.last_error_code"> · </template>
                    <template v-if="item.last_error_code">{{ item.last_error_code }}</template>
                  </span>
                </div>
                <div class="mt-2 h-1.5 overflow-hidden rounded-full bg-white dark:bg-dark-900">
                  <div class="h-full rounded-full bg-sky-500" :style="{ width: endpointLoadWidth(item.total) }" />
                </div>
              </div>
            </div>
            <p v-else class="rounded-lg bg-gray-50 p-4 text-sm text-gray-500 dark:bg-dark-700/50 dark:text-gray-400">
              {{ t('admin.promptAudit.runtime.apiKeyLoadEmpty') }}
            </p>
          </div>
        </div>
      </div>

      <div class="card" data-test="prompt-guard-metrics-card">
        <div class="flex flex-col gap-4 border-b border-gray-100 px-6 py-4 dark:border-dark-700 lg:flex-row lg:items-center lg:justify-between">
          <div>
            <h3 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.promptAudit.runtime.guardMetrics') }}</h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.runtime.guardMetricsHint') }}</p>
          </div>
          <span class="inline-flex w-fit items-center rounded-full bg-gray-100 px-2.5 py-1 text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300">
            {{ t('admin.promptAudit.runtime.dependenciesValue', { database: runtime.database_status, redis: runtime.redis_status }) }}
          </span>
        </div>

        <div class="grid grid-cols-1 gap-6 p-6 xl:grid-cols-[minmax(0,1fr)_minmax(280px,0.35fr)]">
          <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <div v-for="metric in guardMetricItems" :key="metric.label" class="rounded-lg bg-gray-50 p-3 dark:bg-dark-700/50">
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ metric.label }}</p>
              <p class="mt-2 text-xl font-semibold tabular-nums text-gray-900 dark:text-white">{{ metric.value }}</p>
            </div>
          </div>

          <div class="min-w-0 border-t border-gray-100 pt-5 dark:border-dark-700 xl:border-l xl:border-t-0 xl:pl-6 xl:pt-0">
            <p class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.promptAudit.runtime.latest') }}</p>
            <p class="mt-2 text-sm text-gray-600 dark:text-gray-300">
              {{ runtime.last_processed_at ? formatDate(runtime.last_processed_at) : t('admin.promptAudit.common.never') }}
            </p>
            <p v-if="runtime.last_error_code" class="mt-2 break-words text-sm text-red-600 dark:text-red-300">
              {{ runtime.last_error_code }}<span v-if="runtime.last_error_message"> · {{ runtime.last_error_message }}</span>
            </p>
            <div v-if="endpointProbes.length > 0" class="mt-4 space-y-2">
              <div
                v-for="probe in endpointProbes"
                :key="probe.id"
                class="flex min-w-0 items-center justify-between gap-3 rounded-lg bg-gray-50 px-3 py-2 text-xs dark:bg-dark-700/50"
              >
                <span class="min-w-0 truncate font-medium text-gray-700 dark:text-gray-200">{{ probe.id }}</span>
                <span class="flex-shrink-0" :class="probe.value.ok ? 'text-emerald-700 dark:text-emerald-300' : 'text-red-700 dark:text-red-300'">
                  {{ probe.value.status }} · {{ formatNumber(probe.value.latency_ms) }} ms
                </span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { PromptAuditEndpointProtocol, PromptAuditRuntime, PromptEndpointLoad } from '../types'

type OverviewIcon = 'shield' | 'key' | 'inbox' | 'chart'

interface OverviewItem {
  key: string
  label: string
  value: string
  meta: string
  icon: OverviewIcon
  iconClass: string
  badge?: string
  badgeClass?: string
}

const props = defineProps<{ runtime: PromptAuditRuntime | null; loading: boolean; error: string }>()
defineEmits<{ (event: 'refresh'): void }>()
const { t, locale } = useI18n()

const endpointLoads = computed<PromptEndpointLoad[]>(() => (
  [...(props.runtime?.endpoint_loads ?? [])].sort((a, b) => a.index - b.index)
))

const configuredKeyCount = computed(() => endpointLoads.value.filter((item) => item.key_configured).length)
const enabledEndpointCount = computed(() => endpointLoads.value.filter((item) => item.enabled).length)
const activeEndpointCalls = computed(() => endpointLoads.value.reduce((total, item) => total + item.active, 0))
const endpointCallTotal = computed(() => endpointLoads.value.reduce((total, item) => total + item.total, 0))
const endpointMaxTotal = computed(() => Math.max(1, ...endpointLoads.value.map((item) => item.total || 0)))

const endpointLoadSummary = computed(() => t('admin.promptAudit.runtime.apiKeyLoadSummary', {
  endpoints: enabledEndpointCount.value,
  keys: configuredKeyCount.value,
  active: formatNumber(activeEndpointCalls.value),
}))

const queueUsagePercent = computed(() => {
  const runtime = props.runtime
  if (!runtime || runtime.queue_capacity <= 0) return 0
  return Math.min(100, Math.max(0, Math.round(runtime.queue.active * 100 / runtime.queue_capacity)))
})

const overviewItems = computed<OverviewItem[]>(() => {
  const runtime = props.runtime
  if (!runtime) return []
  return [
    {
      key: 'status',
      label: t('admin.promptAudit.runtime.process'),
      value: t(`admin.promptAudit.status.${runtime.process_status}`),
      meta: t(`admin.promptAudit.mode.${runtime.effective_mode}`),
      icon: 'shield',
      iconClass: runtime.process_status === 'running'
        ? 'bg-emerald-50 text-emerald-600 dark:bg-emerald-900/20 dark:text-emerald-300'
        : 'bg-amber-50 text-amber-600 dark:bg-amber-900/20 dark:text-amber-300',
      badge: `${runtime.active_config_version} / ${runtime.expected_config_version}`,
      badgeClass: runtime.active_config_version === runtime.expected_config_version
        ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300'
        : 'bg-amber-50 text-amber-700 dark:bg-amber-900/20 dark:text-amber-300',
    },
    {
      key: 'keys',
      label: t('admin.promptAudit.runtime.apiKeys'),
      value: t('admin.promptAudit.runtime.apiKeyCount', { count: configuredKeyCount.value }),
      meta: t('admin.promptAudit.runtime.endpointCount', { count: enabledEndpointCount.value }),
      icon: 'key',
      iconClass: 'bg-sky-50 text-sky-600 dark:bg-sky-900/20 dark:text-sky-300',
    },
    {
      key: 'queue',
      label: t('admin.promptAudit.runtime.queue'),
      value: `${formatNumber(runtime.queue.active)} / ${formatNumber(runtime.queue_capacity)}`,
      meta: t('admin.promptAudit.runtime.queueMeta', { queued: formatNumber(runtime.queue.queued), retry: formatNumber(runtime.queue.retry) }),
      icon: 'inbox',
      iconClass: 'bg-violet-50 text-violet-600 dark:bg-violet-900/20 dark:text-violet-300',
    },
    {
      key: 'audits',
      label: t('admin.promptAudit.runtime.auditCalls'),
      value: formatNumber(runtime.guard_metrics.total),
      meta: t('admin.promptAudit.runtime.auditCallMeta', {
        blocked: formatNumber(runtime.guard_metrics.blocked),
        errors: formatNumber(runtime.guard_metrics.unavailable + runtime.guard_metrics.invalid),
        calls: formatNumber(endpointCallTotal.value),
      }),
      icon: 'chart',
      iconClass: 'bg-amber-50 text-amber-600 dark:bg-amber-900/20 dark:text-amber-300',
    },
  ]
})

const workerMetricItems = computed(() => {
  const runtime = props.runtime
  if (!runtime) return []
  return [
    { key: 'workers', label: t('admin.promptAudit.runtime.workerActive'), value: `${formatNumber(runtime.worker_active)} / ${formatNumber(runtime.worker_total)}`, class: 'bg-sky-50 dark:bg-sky-900/10', valueClass: 'text-sky-700 dark:text-sky-300' },
    { key: 'queued', label: t('admin.promptAudit.runtime.queued'), value: formatNumber(runtime.queue.queued), class: 'bg-gray-50 dark:bg-dark-700/50', valueClass: 'text-gray-900 dark:text-white' },
    { key: 'processing', label: t('admin.promptAudit.runtime.processing'), value: formatNumber(runtime.queue.processing), class: 'bg-violet-50 dark:bg-violet-900/10', valueClass: 'text-violet-700 dark:text-violet-300' },
    { key: 'retry', label: t('admin.promptAudit.runtime.retrying'), value: formatNumber(runtime.queue.retry), class: 'bg-amber-50 dark:bg-amber-900/10', valueClass: 'text-amber-700 dark:text-amber-300' },
    { key: 'done', label: t('admin.promptAudit.runtime.done'), value: formatNumber(runtime.queue.done), class: 'bg-emerald-50 dark:bg-emerald-900/10', valueClass: 'text-emerald-700 dark:text-emerald-300' },
    { key: 'failed', label: t('admin.promptAudit.runtime.failed'), value: formatNumber(runtime.queue.failed), class: 'bg-red-50 dark:bg-red-900/10', valueClass: 'text-red-700 dark:text-red-300' },
  ]
})

const guardMetricItems = computed(() => {
  const metrics = props.runtime?.guard_metrics
  if (!metrics) return []
  return [
    { label: t('admin.promptAudit.metrics.total'), value: formatNumber(metrics.total) },
    { label: t('admin.promptAudit.metrics.allowed'), value: formatNumber(metrics.allowed) },
    { label: t('admin.promptAudit.metrics.flagged'), value: formatNumber(metrics.flagged) },
    { label: t('admin.promptAudit.metrics.blocked'), value: formatNumber(metrics.blocked) },
    { label: t('admin.promptAudit.metrics.unavailable'), value: formatNumber(metrics.unavailable) },
    { label: t('admin.promptAudit.metrics.timeouts'), value: formatNumber(metrics.timeouts) },
    { label: t('admin.promptAudit.metrics.failovers'), value: formatNumber(metrics.failovers) },
    { label: 'P95', value: `${formatNumber(metrics.latency_p95_ms ?? 0)} ms` },
  ]
})

const endpointProbes = computed(() => Object.entries(props.runtime?.endpoints ?? {})
  .sort(([left], [right]) => left.localeCompare(right))
  .map(([id, value]) => ({ id, value })))

function formatNumber(value: number): string {
  return new Intl.NumberFormat(locale.value).format(Number.isFinite(value) ? value : 0)
}

function formatDate(value: string): string {
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'medium' }).format(new Date(value))
}

function endpointLoadWidth(total: number): string {
  if (total <= 0) return '0%'
  return `${Math.max(4, Math.round(total * 100 / endpointMaxTotal.value))}%`
}

function protocolLabel(protocol: PromptAuditEndpointProtocol): string {
  return t(`admin.promptAudit.pool.protocols.${protocol}`)
}

function endpointStatusLabel(status: string): string {
  const key = `admin.promptAudit.runtime.endpointStatus.${status}`
  const label = t(key)
  return label === key ? status : label
}

function endpointStatusDotClass(status: string): string {
  if (status === 'healthy') return 'bg-emerald-500'
  if (status === 'error') return 'bg-red-500'
  if (status === 'disabled') return 'bg-gray-400'
  return 'bg-amber-400'
}
</script>
