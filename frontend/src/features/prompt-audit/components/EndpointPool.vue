<template>
  <section aria-labelledby="prompt-pool-title">
    <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
      <div>
        <h3 id="prompt-pool-title" class="text-base font-semibold text-gray-950 dark:text-white">{{ t('admin.promptAudit.pool.title') }}</h3>
        <p class="mt-1 text-sm text-gray-500 dark:text-dark-300">{{ t('admin.promptAudit.pool.description') }}</p>
      </div>
      <button type="button" class="btn btn-primary inline-flex items-center justify-center gap-2" data-test="add-endpoint" @click="openCreate">
        <Icon name="plus" size="sm" />
        {{ t('admin.promptAudit.pool.add') }}
      </button>
    </div>

    <form
      v-if="editing"
      class="mt-5 overflow-hidden rounded-lg border border-primary-200 bg-white shadow-sm dark:border-primary-900/70 dark:bg-dark-800"
      data-test="endpoint-editor"
      @submit.prevent="saveEditor"
    >
      <div class="flex flex-col gap-3 border-b border-gray-100 bg-gray-50 px-4 py-4 dark:border-dark-700 dark:bg-dark-800/60 sm:flex-row sm:items-center sm:justify-between">
        <div class="flex items-start gap-3">
          <span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300">
            <Icon :name="isGroqSafeguard ? 'shield' : 'server'" size="md" />
          </span>
          <div>
            <h4 class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ editingIndex < 0 ? t('admin.promptAudit.pool.add') : t('admin.promptAudit.pool.edit') }}
            </h4>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.pool.editorHint') }}</p>
          </div>
        </div>
        <label class="flex cursor-pointer items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
          <input v-model="editing.enabled" type="checkbox" data-test="endpoint-editor-enabled" />
          {{ t('admin.promptAudit.pool.enabled') }}
        </label>
      </div>

      <div class="space-y-5 p-4 sm:p-5">
        <fieldset>
          <legend class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.promptAudit.pool.protocol') }}</legend>
          <div class="mt-3 grid gap-3 md:grid-cols-2">
            <button
              v-for="protocol in PROMPT_AUDIT_PROTOCOLS"
              :key="protocol.id"
              type="button"
              role="radio"
              :aria-checked="editing.protocol === protocol.id"
              :data-test="`endpoint-protocol-${protocol.id}`"
              class="min-h-24 rounded-lg border p-4 text-left transition-colors"
              :class="editing.protocol === protocol.id
                ? 'border-primary-300 bg-primary-50 text-primary-950 shadow-sm dark:border-primary-700 dark:bg-primary-900/20 dark:text-primary-100'
                : 'border-gray-200 hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-700/60'"
              @click="selectProtocol(protocol.id)"
            >
              <span class="flex items-start justify-between gap-3">
                <span class="flex min-w-0 items-start gap-3">
                  <Icon :name="protocol.id === 'groq_safeguard' ? 'shield' : 'server'" size="md" class="mt-0.5 shrink-0 text-primary-600 dark:text-primary-300" />
                  <span class="min-w-0">
                    <span class="block text-sm font-semibold">{{ t(`admin.promptAudit.pool.protocols.${protocol.id}`) }}</span>
                    <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t(`admin.promptAudit.pool.protocolDescriptions.${protocol.id}`) }}</span>
                  </span>
                </span>
                <span
                  class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full border"
                  :class="editing.protocol === protocol.id ? 'border-primary-500 bg-primary-500 text-white' : 'border-gray-300 text-transparent dark:border-dark-500'"
                >
                  <Icon name="check" size="xs" :stroke-width="2" />
                </span>
              </span>
            </button>
          </div>
        </fieldset>

        <div v-if="isGroqSafeguard" class="flex items-start gap-3 rounded-lg border border-sky-200 bg-sky-50 px-4 py-3 text-sm text-sky-900 dark:border-sky-900/70 dark:bg-sky-950/30 dark:text-sky-200" data-test="groq-safeguard-notice">
          <Icon name="infoCircle" size="md" class="mt-0.5 shrink-0" />
          <div>
            <p class="font-medium">{{ t('admin.promptAudit.pool.groqScopeTitle') }}</p>
            <p class="mt-1 text-xs leading-5">{{ t('admin.promptAudit.pool.groqScopeHint') }}</p>
          </div>
        </div>

        <div class="grid gap-4 sm:grid-cols-2">
          <label class="space-y-1.5 text-sm text-gray-700 dark:text-dark-200">
            <span>{{ t('admin.promptAudit.pool.name') }}</span>
            <input v-model.trim="editing.name" class="input w-full" required :aria-label="t('admin.promptAudit.pool.name')" />
          </label>
          <label class="space-y-1.5 text-sm text-gray-700 dark:text-dark-200">
            <span>{{ t('admin.promptAudit.pool.id') }}</span>
            <input v-model.trim="editing.id" class="input w-full font-mono" required :disabled="editingIndex >= 0" :aria-label="t('admin.promptAudit.pool.id')" />
          </label>
          <label class="space-y-1.5 text-sm text-gray-700 dark:text-dark-200 sm:col-span-2">
            <span>{{ t('admin.promptAudit.pool.baseUrl') }}</span>
            <input v-model.trim="editing.base_url" class="input w-full font-mono text-sm" required type="url" inputmode="url" :aria-label="t('admin.promptAudit.pool.baseUrl')" />
            <span class="block text-xs text-gray-500 dark:text-dark-400">{{ t(`admin.promptAudit.pool.baseUrlHints.${editing.protocol}`) }}</span>
          </label>
          <label class="space-y-1.5 text-sm text-gray-700 dark:text-dark-200 sm:col-span-2">
            <span>{{ t('admin.promptAudit.pool.model') }}</span>
            <input
              v-model.trim="editing.model"
              class="input w-full font-mono text-sm"
              required
              :readonly="isGroqSafeguard"
              :aria-readonly="isGroqSafeguard"
              :aria-label="t('admin.promptAudit.pool.model')"
            />
            <span class="block text-xs text-gray-500 dark:text-dark-400">{{ t(isGroqSafeguard ? 'admin.promptAudit.pool.groqModelHint' : 'admin.promptAudit.pool.qwenModelHint') }}</span>
          </label>
        </div>

        <div class="border-t border-gray-100 pt-5 dark:border-dark-700">
          <div class="mb-3 flex items-start gap-3">
            <Icon name="key" size="md" class="mt-0.5 shrink-0 text-gray-400" />
            <div>
              <p class="text-sm font-semibold text-gray-900 dark:text-white">{{ credentialTitle }}</p>
              <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ credentialHint }}</p>
            </div>
          </div>
          <label class="block space-y-1.5 text-sm text-gray-700 dark:text-dark-200">
            <span>{{ t('admin.promptAudit.pool.apiKey') }}</span>
            <input
              v-model="editing.token"
              class="input w-full font-mono text-sm"
              type="password"
              autocomplete="new-password"
              :placeholder="editing.has_token ? t('admin.promptAudit.pool.keepSecret') : isGroqSafeguard ? 'gsk_...' : ''"
              :aria-label="t('admin.promptAudit.pool.apiKey')"
            />
          </label>
          <label v-if="editing.has_token" class="mt-3 flex w-fit cursor-pointer items-center gap-2 text-sm text-red-600 dark:text-red-300">
            <input v-model="editing.clear_token" type="checkbox" :aria-label="t('admin.promptAudit.pool.clearSecret')" />
            {{ t('admin.promptAudit.pool.clearSecret') }}
          </label>
        </div>

        <details class="border-t border-gray-100 pt-4 dark:border-dark-700">
          <summary class="flex cursor-pointer list-none items-center justify-between gap-3 text-sm font-medium text-gray-800 dark:text-gray-100">
            <span>{{ t('admin.promptAudit.pool.advanced') }}</span>
            <Icon name="chevronDown" size="sm" class="text-gray-400" />
          </summary>
          <div class="mt-4 grid gap-4 sm:grid-cols-2">
            <label class="space-y-1.5 text-sm text-gray-700 dark:text-dark-200">
              <span>{{ t('admin.promptAudit.pool.timeout') }}</span>
              <input v-model.number="editing.timeout_ms" class="input w-full" type="number" min="100" max="30000" required :aria-label="t('admin.promptAudit.pool.timeout')" />
            </label>
            <label class="space-y-1.5 text-sm text-gray-700 dark:text-dark-200">
              <span>{{ inputLimitLabel }}</span>
              <input v-model.number="editing.input_limit" class="input w-full" type="number" min="128" max="100000" required :aria-label="inputLimitLabel" />
              <span class="block text-xs leading-5 text-gray-500 dark:text-gray-400">{{ inputLimitHint }}</span>
            </label>
            <label v-if="isGroqSafeguard" class="space-y-1.5 text-sm text-gray-700 dark:text-dark-200">
              <span>{{ t('admin.promptAudit.pool.tpmLimit') }}</span>
              <input
                v-model.number="editing.tpm_limit"
                class="input w-full"
                type="number"
                :min="MIN_GROQ_TPM_LIMIT"
                :max="MAX_GROQ_TPM_LIMIT"
                required
                :aria-label="t('admin.promptAudit.pool.tpmLimit')"
              />
              <span class="block text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.pool.tpmLimitHint') }}</span>
            </label>
          </div>
        </details>

        <div v-if="editorProbeResult" class="rounded-lg px-3 py-2 text-sm" :class="editorProbeResult.ok ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300' : 'bg-red-50 text-red-700 dark:bg-red-950/30 dark:text-red-300'" data-test="endpoint-editor-probe-result">
          {{ t('admin.promptAudit.pool.probeResult', { status: editorProbeResult.status, http: editorProbeResult.http_status || '-', latency: editorProbeResult.latency_ms }) }}
          <span v-if="editorProbeResult.message"> · {{ editorProbeResult.message }}</span>
        </div>

        <div class="flex flex-col gap-3 border-t border-gray-100 pt-4 dark:border-dark-700 sm:flex-row sm:items-center sm:justify-between">
          <p v-if="editorError" class="text-sm text-red-600 dark:text-red-300" data-test="endpoint-editor-error">{{ editorError }}</p>
          <span v-else class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.pool.saveToDraftHint') }}</span>
          <div class="flex flex-wrap justify-end gap-2">
            <button type="button" class="btn btn-secondary" @click="closeEditor">{{ t('common.cancel') }}</button>
            <button type="button" class="btn btn-secondary inline-flex items-center gap-2" :disabled="!canProbeEditor || editorProbing" data-test="probe-endpoint-editor" @click="probeEditing">
              <Icon name="beaker" size="sm" :class="editorProbing ? 'animate-pulse' : ''" />
              {{ editorProbing ? t('admin.promptAudit.pool.probing') : t('admin.promptAudit.pool.probe') }}
            </button>
            <button type="submit" class="btn btn-primary inline-flex items-center gap-2" :disabled="Boolean(editorError)" data-test="save-endpoint">
              <Icon name="check" size="sm" />
              {{ t('common.save') }}
            </button>
          </div>
        </div>
      </div>
    </form>

    <div v-if="endpoints.length === 0 && !editing" class="mt-5 rounded-lg border border-dashed border-gray-300 px-5 py-10 text-center text-sm text-gray-500 dark:border-dark-600 dark:bg-dark-900/20 dark:text-dark-300">
      <Icon name="server" size="lg" class="mx-auto text-gray-300 dark:text-dark-500" />
      <p class="mt-3 font-medium text-gray-700 dark:text-gray-200">{{ t('admin.promptAudit.pool.empty') }}</p>
      <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.pool.emptyHint') }}</p>
    </div>

    <div v-else-if="endpoints.length > 0" class="mt-5 overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
      <article
        v-for="endpoint in endpoints"
        :key="endpoint.id"
        :data-test="`endpoint-${endpoint.id}`"
        class="grid gap-4 border-b border-gray-100 px-4 py-4 last:border-b-0 dark:border-dark-700 lg:grid-cols-[minmax(220px,1.1fr)_minmax(240px,1.2fr)_minmax(190px,.8fr)_auto] lg:items-center"
      >
        <div class="flex min-w-0 items-center gap-3">
          <button
            type="button"
            role="switch"
            :aria-checked="endpoint.enabled"
            :aria-label="t('admin.promptAudit.pool.toggleNode', { name: endpoint.name })"
            class="relative inline-flex h-6 w-11 shrink-0 cursor-pointer items-center rounded-full border-2 border-transparent transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 focus-visible:ring-offset-2"
            :class="endpoint.enabled ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'"
            @click="toggleEndpoint(endpoint.id)"
          >
            <span class="pointer-events-none inline-block h-5 w-5 rounded-full bg-white shadow transition-transform" :class="endpoint.enabled ? 'translate-x-5' : 'translate-x-0'" />
          </button>
          <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-300">
            <Icon :name="endpoint.protocol === 'groq_safeguard' ? 'shield' : 'server'" size="sm" />
          </span>
          <div class="min-w-0">
            <div class="flex min-w-0 items-center gap-2">
              <p class="truncate text-sm font-semibold text-gray-950 dark:text-white">{{ endpoint.name }}</p>
              <span class="h-2 w-2 shrink-0 rounded-full" :class="endpoint.enabled ? 'bg-emerald-500' : 'bg-gray-300 dark:bg-dark-500'" />
            </div>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t(`admin.promptAudit.pool.protocols.${endpoint.protocol}`) }}</p>
          </div>
        </div>

        <div class="min-w-0">
          <p class="truncate font-mono text-sm text-gray-800 dark:text-gray-100" :title="endpoint.model">{{ endpoint.model }}</p>
          <p class="mt-1 truncate font-mono text-xs text-gray-500 dark:text-gray-400" :title="endpoint.base_url">{{ endpoint.base_url }}</p>
        </div>

        <div class="min-w-0">
          <div class="flex items-center gap-2 text-xs font-medium" :class="credentialClass(endpoint)">
            <span class="h-2 w-2 shrink-0 rounded-full" :class="credentialDotClass(endpoint)" />
            <span class="truncate">{{ credentialLabel(endpoint) }}</span>
          </div>
          <p v-if="probeResults[endpoint.id]" class="mt-1 truncate text-xs" :class="probeResults[endpoint.id].ok ? 'text-emerald-600 dark:text-emerald-300' : 'text-red-600 dark:text-red-300'" :title="probeResults[endpoint.id].message">
            {{ probeResults[endpoint.id].status }} · {{ probeResults[endpoint.id].latency_ms }} ms
          </p>
          <p v-else class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ endpointLimitSummary(endpoint) }}</p>
        </div>

        <div class="flex items-center justify-end gap-1">
          <button type="button" class="btn btn-secondary btn-sm inline-flex items-center gap-2" :disabled="probingIds.includes(endpoint.id) || !canProbe(endpoint)" @click="$emit('probe', endpoint)">
            <Icon name="beaker" size="xs" :class="probingIds.includes(endpoint.id) ? 'animate-pulse' : ''" />
            {{ probingIds.includes(endpoint.id) ? t('admin.promptAudit.pool.probing') : t('admin.promptAudit.pool.probe') }}
          </button>
          <button type="button" class="flex h-9 w-9 items-center justify-center rounded-md text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:text-gray-400 dark:hover:bg-dark-700 dark:hover:text-white" :title="t('common.edit')" :aria-label="t('common.edit')" @click="openEdit(endpoint)">
            <Icon name="edit" size="sm" />
          </button>
          <button type="button" class="flex h-9 w-9 items-center justify-center rounded-md text-red-500 transition-colors hover:bg-red-50 hover:text-red-700 dark:text-red-300 dark:hover:bg-red-950/30" :title="t('common.delete')" :aria-label="t('common.delete')" @click="removeEndpoint(endpoint)">
            <Icon name="trash" size="sm" />
          </button>
        </div>
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { PromptAuditEndpointDraft, PromptAuditEndpointProtocol, PromptProbeResult } from '../types'
import {
  applyEndpointProtocolPreset,
  cloneData,
  createDefaultEndpoint,
  DEFAULT_GROQ_SAFEGUARD_MODEL,
  DEFAULT_GROQ_TPM_LIMIT,
  MAX_GROQ_TPM_LIMIT,
  MIN_GROQ_TPM_LIMIT,
  PROMPT_AUDIT_PROTOCOLS,
} from '../viewModel'

const props = defineProps<{
  endpoints: PromptAuditEndpointDraft[]
  probeResults: Record<string, PromptProbeResult>
  probingIds: string[]
}>()
const emit = defineEmits<{
  (event: 'update:endpoints', value: PromptAuditEndpointDraft[]): void
  (event: 'probe', endpoint: PromptAuditEndpointDraft): void
}>()
const { t } = useI18n()
const editing = ref<PromptAuditEndpointDraft | null>(null)
const editingIndex = ref(-1)

const isGroqSafeguard = computed(() => editing.value?.protocol === 'groq_safeguard')
const editorProbing = computed(() => Boolean(editing.value && props.probingIds.includes(editing.value.id)))
const editorProbeResult = computed(() => editing.value ? props.probeResults[editing.value.id] : undefined)
const inputLimitLabel = computed(() => t(
  isGroqSafeguard.value
    ? 'admin.promptAudit.pool.sessionTokenBudget'
    : 'admin.promptAudit.pool.inputLimit',
))
const inputLimitHint = computed(() => t(
  isGroqSafeguard.value
    ? 'admin.promptAudit.pool.sessionTokenBudgetHint'
    : 'admin.promptAudit.pool.inputLimitHint',
))
const editorError = computed(() => {
  const endpoint = editing.value
  if (!endpoint) return ''
  const id = endpoint.id.trim()
  if (!id || !endpoint.name.trim() || !endpoint.base_url.trim() || !endpoint.model.trim()) {
    return t('admin.promptAudit.pool.validation.required')
  }
  if (props.endpoints.some((item, index) => index !== editingIndex.value && item.id.trim() === id)) {
    return t('admin.promptAudit.pool.validation.duplicateId')
  }
  if (!validURL(endpoint.base_url)) return t('admin.promptAudit.pool.validation.baseUrl')
  if (endpoint.timeout_ms < 100 || endpoint.timeout_ms > 30000) return t('admin.promptAudit.pool.validation.timeout')
  if (endpoint.input_limit < 128 || endpoint.input_limit > 100000) {
    return t(endpoint.protocol === 'groq_safeguard'
      ? 'admin.promptAudit.pool.validation.sessionTokenBudget'
      : 'admin.promptAudit.pool.validation.inputLimit')
  }
  if (
    endpoint.protocol === 'groq_safeguard' &&
    (endpoint.tpm_limit < MIN_GROQ_TPM_LIMIT || endpoint.tpm_limit > MAX_GROQ_TPM_LIMIT)
  ) {
    return t('admin.promptAudit.pool.validation.tpmLimit')
  }
  if (endpoint.enabled && endpoint.protocol === 'groq_safeguard' && !hasCredential(endpoint)) {
    return t('admin.promptAudit.pool.validation.groqCredential')
  }
  return ''
})
const canProbeEditor = computed(() => {
  const endpoint = editing.value
  if (!endpoint) return false
  return Boolean(
    endpoint.id.trim() &&
    endpoint.name.trim() &&
    endpoint.model.trim() &&
    validURL(endpoint.base_url) &&
    endpoint.timeout_ms >= 100 &&
    endpoint.timeout_ms <= 30000 &&
    endpoint.input_limit >= 128 &&
    endpoint.input_limit <= 100000 &&
    (
      endpoint.protocol !== 'groq_safeguard' ||
      (endpoint.tpm_limit >= MIN_GROQ_TPM_LIMIT && endpoint.tpm_limit <= MAX_GROQ_TPM_LIMIT)
    ) &&
    (endpoint.protocol !== 'groq_safeguard' || hasCredential(endpoint)),
  )
})
const credentialTitle = computed(() => t(isGroqSafeguard.value ? 'admin.promptAudit.pool.groqCredentialTitle' : 'admin.promptAudit.pool.qwenCredentialTitle'))
const credentialHint = computed(() => t(isGroqSafeguard.value ? 'admin.promptAudit.pool.groqCredentialHint' : 'admin.promptAudit.pool.qwenCredentialHint'))

function openCreate() {
  editingIndex.value = -1
  editing.value = createDefaultEndpoint(props.endpoints.length + 1)
}

function openEdit(endpoint: PromptAuditEndpointDraft) {
  editingIndex.value = props.endpoints.findIndex((item) => item.id === endpoint.id)
  editing.value = cloneData(endpoint)
  if (editing.value.protocol === 'groq_safeguard') {
    editing.value.model = DEFAULT_GROQ_SAFEGUARD_MODEL
    editing.value.tpm_limit ||= DEFAULT_GROQ_TPM_LIMIT
  } else {
    editing.value.tpm_limit = 0
  }
}

function closeEditor() {
  editing.value = null
  editingIndex.value = -1
}

function selectProtocol(protocol: PromptAuditEndpointProtocol) {
  if (!editing.value || editing.value.protocol === protocol) return
  const clearStoredCredential = editing.value.has_token && !editing.value.clear_token
  editing.value = {
    ...applyEndpointProtocolPreset(editing.value, protocol),
    token: '',
    has_token: false,
    token_status: 'missing',
    clear_token: clearStoredCredential,
  }
}

function probeEditing() {
  if (!editing.value || !canProbeEditor.value || editorProbing.value) return
  emit('probe', cloneData(editing.value))
}

function saveEditor() {
  if (!editing.value || editorError.value) return
  const next = props.endpoints.map((item) => cloneData(item))
  const value = cloneData(editing.value)
  if (value.protocol === 'groq_safeguard') {
    value.model = DEFAULT_GROQ_SAFEGUARD_MODEL
    value.tpm_limit ||= DEFAULT_GROQ_TPM_LIMIT
  } else {
    value.tpm_limit = 0
  }
  if (value.token.trim()) value.clear_token = false
  if (editingIndex.value < 0) next.push(value)
  else next.splice(editingIndex.value, 1, value)
  emit('update:endpoints', next)
  closeEditor()
}

function toggleEndpoint(id: string) {
  emit('update:endpoints', props.endpoints.map((item) => item.id === id ? { ...item, enabled: !item.enabled } : cloneData(item)))
}

function removeEndpoint(endpoint: PromptAuditEndpointDraft) {
  if (!window.confirm(t('admin.promptAudit.pool.deleteConfirm', { name: endpoint.name }))) return
  emit('update:endpoints', props.endpoints.filter((item) => item.id !== endpoint.id).map((item) => cloneData(item)))
  if (editing.value?.id === endpoint.id) closeEditor()
}

function validURL(value: string): boolean {
  try {
    const parsed = new URL(value.trim())
    return parsed.protocol === 'http:' || parsed.protocol === 'https:'
  } catch {
    return false
  }
}

function hasCredential(endpoint: PromptAuditEndpointDraft): boolean {
  return Boolean(endpoint.token.trim() || (endpoint.has_token && !endpoint.clear_token))
}

function canProbe(endpoint: PromptAuditEndpointDraft): boolean {
  return endpoint.protocol !== 'groq_safeguard' || hasCredential(endpoint)
}

function endpointLimitSummary(endpoint: PromptAuditEndpointDraft): string {
  if (endpoint.protocol === 'groq_safeguard') {
    return t('admin.promptAudit.pool.groqLimitSummary', {
      timeout: endpoint.timeout_ms,
      tokens: endpoint.input_limit,
      tpm: endpoint.tpm_limit || DEFAULT_GROQ_TPM_LIMIT,
    })
  }
  return t('admin.promptAudit.pool.qwenLimitSummary', {
    timeout: endpoint.timeout_ms,
    chars: endpoint.input_limit,
  })
}

function credentialLabel(endpoint: PromptAuditEndpointDraft): string {
  if (hasCredential(endpoint)) return t('admin.promptAudit.pool.configured')
  return endpoint.protocol === 'groq_safeguard'
    ? t('admin.promptAudit.pool.credentialRequired')
    : t('admin.promptAudit.pool.credentialOptional')
}

function credentialClass(endpoint: PromptAuditEndpointDraft): string {
  if (hasCredential(endpoint)) return 'text-emerald-700 dark:text-emerald-300'
  return endpoint.protocol === 'groq_safeguard'
    ? 'text-red-600 dark:text-red-300'
    : 'text-gray-500 dark:text-gray-400'
}

function credentialDotClass(endpoint: PromptAuditEndpointDraft): string {
  if (hasCredential(endpoint)) return 'bg-emerald-500'
  return endpoint.protocol === 'groq_safeguard' ? 'bg-red-500' : 'bg-gray-300 dark:bg-dark-500'
}
</script>
