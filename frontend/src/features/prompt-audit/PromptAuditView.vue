<template>
  <AppLayout>
    <div class="space-y-6 pb-8">
      <header class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('admin.promptAudit.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.description') }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-2 lg:justify-end">
          <div v-if="draft" class="mr-1 text-xs text-gray-500 dark:text-dark-400 lg:text-right">
            <p>{{ t('admin.promptAudit.configVersion', { version: draft.config_version }) }}</p>
            <p v-if="draft.updated_at" class="mt-1">{{ formatDate(draft.updated_at) }}</p>
          </div>
          <button type="button" class="btn btn-secondary inline-flex items-center gap-2" :disabled="loading.runtime" data-test="refresh-runtime" @click="loadRuntime">
            <Icon name="refresh" size="sm" :class="loading.runtime ? 'animate-spin' : ''" />
            {{ t('admin.promptAudit.actions.refresh') }}
          </button>
          <button v-if="draft" type="button" class="btn btn-primary inline-flex items-center gap-2" data-test="open-settings" @click="openSettings()">
            <Icon name="cog" size="sm" />
            {{ t('admin.promptAudit.settings.open') }}
          </button>
        </div>
      </header>

      <div v-if="loadErrors.config && !draft" role="alert" class="card border-red-200 bg-red-50 p-5 dark:border-red-900 dark:bg-red-950/30">
        <p class="text-sm text-red-700 dark:text-red-300">{{ loadErrors.config }}</p>
        <button type="button" class="btn btn-secondary btn-sm mt-3" @click="loadConfig">{{ t('admin.promptAudit.actions.retry') }}</button>
      </div>

      <template v-else>
        <RuntimeOverview :runtime="runtime" :loading="loading.runtime" :error="loadErrors.runtime" @refresh="loadRuntime" />

        <div
          v-if="draft?.enabled && !draft.store_pass_events"
          data-test="pass-events-disabled-notice"
          role="status"
          class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900 dark:border-amber-900/70 dark:bg-amber-950/30 dark:text-amber-200"
        >
          <span>{{ t('admin.promptAudit.events.passEventsDisabled') }}</span>
          <button type="button" class="btn btn-secondary btn-sm" @click="openSettings('service')">
            {{ t('admin.promptAudit.events.openConfiguration') }}
          </button>
        </div>

        <EventWorkspace
          :events="events.items"
          :total="events.total"
          :page="events.page"
          :page-size="events.page_size"
          :filters="filters"
          :selected-ids="selectedEventIds"
          :loading="loading.events"
          :error="loadErrors.events"
          @filters-change="handleFiltersChanged"
          @search="applyEventFilters"
          @selection="selectedEventIds = $event"
          @page="changePage"
          @page-size="changePageSize"
          @view="openEvent"
          @delete="requestSingleDelete"
          @batch-delete="requestBatchDelete"
          @preview-delete="requestFilterDeletePreview"
        />
      </template>
    </div>

    <BaseDialog :show="settingsOpen && Boolean(draft)" :title="t('admin.promptAudit.settings.title')" width="extra-wide" @close="closeSettings">
      <div v-if="draft" class="space-y-6" data-test="prompt-audit-settings">
        <div class="flex gap-2 overflow-x-auto border-b border-gray-100 pb-3 dark:border-dark-700" role="tablist" :aria-label="t('admin.promptAudit.settings.title')">
          <button
            v-for="tab in settingsTabs"
            :key="tab.id"
            type="button"
            role="tab"
            class="inline-flex whitespace-nowrap rounded-lg px-3 py-2 text-sm font-medium transition-colors"
            :class="activeSettingsTab === tab.id ? 'bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300' : 'text-gray-500 hover:bg-gray-50 hover:text-gray-900 dark:text-gray-400 dark:hover:bg-dark-700 dark:hover:text-white'"
            :aria-selected="activeSettingsTab === tab.id"
            :data-test="`settings-tab-${tab.id}`"
            @click="activeSettingsTab = tab.id"
          >
            {{ tab.label }}
          </button>
        </div>

        <div v-if="activeSettingsTab === 'service'" class="space-y-6" data-test="settings-panel-service">
          <section>
            <h3 class="text-base font-semibold text-gray-950 dark:text-white">{{ t('admin.promptAudit.settings.modeTitle') }}</h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-300">{{ t('admin.promptAudit.settings.modeDescription') }}</p>
            <div class="mt-4 grid gap-3 lg:grid-cols-3">
              <div class="flex items-center justify-between gap-4 rounded-lg border border-gray-100 p-4 dark:border-dark-700">
                <div>
                  <p class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.promptAudit.settings.enabled') }}</p>
                  <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.settings.enabledHint') }}</p>
                </div>
                <SaveToggle :label="t('admin.promptAudit.settings.enabled')" :model-value="draft.enabled" data-test="enabled-toggle" hide-label @update:model-value="setEnabled" />
              </div>
              <div class="flex items-center justify-between gap-4 rounded-lg border border-gray-100 p-4 dark:border-dark-700">
                <div>
                  <p class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.promptAudit.settings.blocking') }}</p>
                  <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.settings.blockingHint') }}</p>
                </div>
                <SaveToggle :label="t('admin.promptAudit.settings.blocking')" :model-value="draft.blocking_enabled" :disabled="!draft.enabled" data-test="blocking-toggle" hide-label @update:model-value="setBlocking" />
              </div>
              <div class="flex items-center justify-between gap-4 rounded-lg border border-gray-100 p-4 dark:border-dark-700">
                <div>
                  <p class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.promptAudit.settings.storePass') }}</p>
                  <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.settings.storePassHint') }}</p>
                </div>
                <SaveToggle :label="t('admin.promptAudit.settings.storePass')" :model-value="draft.store_pass_events" data-test="store-pass-toggle" hide-label @update:model-value="replaceDraft({ ...draft!, store_pass_events: $event })" />
              </div>
            </div>
          </section>

          <div v-if="draft.blocking_enabled" class="flex items-start gap-3 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900 dark:border-amber-900/70 dark:bg-amber-950/30 dark:text-amber-200">
            <Icon name="infoCircle" size="md" class="mt-0.5 shrink-0" />
            <span>{{ t('admin.promptAudit.settings.blockingNotice') }}</span>
          </div>

          <EndpointPool
            :endpoints="draft.endpoints"
            :probe-results="probeResults"
            :probing-ids="probingIds"
            @update:endpoints="updateEndpoints"
            @probe="runProbe"
          />
        </div>

        <div v-else-if="activeSettingsTab === 'scope'" data-test="settings-panel-scope">
          <div v-if="loadErrors.groups" role="alert" class="mb-5 rounded-lg bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:bg-amber-950/30 dark:text-amber-200">{{ loadErrors.groups }}</div>
          <PolicyPanel section="scope" :draft="draft" :groups="groups" @update:draft="replaceDraft" />
        </div>

        <div v-else-if="activeSettingsTab === 'policy'" data-test="settings-panel-policy">
          <SafeguardPolicyPanel
            :policy="draft.groq_safeguard_policy"
            :default-policy="draft.groq_safeguard_default_policy"
            :scanners="draft.scanners"
            :has-groq="hasGroqEndpoint"
            :has-qwen="hasQwenEndpoint"
            :preview="safeguardPolicyPreview"
            :previewing="loading.policyPreview"
            :preview-error="safeguardPolicyPreviewError"
            @update:policy="updateSafeguardPolicy"
            @preview="previewSafeguardPolicy"
          />
        </div>

        <div v-else data-test="settings-panel-runtime">
          <PolicyPanel section="runtime" :draft="draft" :groups="groups" @update:draft="replaceDraft" />
        </div>
      </div>

      <template #footer>
        <div v-if="draft" class="flex w-full flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div class="min-w-0 text-sm">
            <p v-if="configValidationMessage" class="text-red-600 dark:text-red-300" data-test="config-validation">{{ configValidationMessage }}</p>
            <p v-else :class="dirty ? 'text-amber-700 dark:text-amber-300' : 'text-gray-500 dark:text-dark-400'">
              {{ dirty ? t('admin.promptAudit.saveBar.dirty') : t('admin.promptAudit.saveBar.synced') }}
            </p>
          </div>
          <div class="flex flex-wrap justify-end gap-2">
            <button type="button" class="btn btn-secondary" :disabled="!dirty || loading.saving" @click="resetDraft">{{ t('common.reset') }}</button>
            <button type="button" class="btn btn-secondary" @click="closeSettings">{{ t('common.cancel') }}</button>
            <button type="button" class="btn btn-primary inline-flex items-center gap-2" :disabled="!dirty || loading.saving || Boolean(configValidationMessage)" data-test="save-config" @click="saveConfig">
              <Icon v-if="loading.saving" name="refresh" size="sm" class="animate-spin" />
              <Icon v-else name="check" size="sm" />
              {{ loading.saving ? t('common.saving') : t('common.save') }}
            </button>
          </div>
        </div>
      </template>
    </BaseDialog>

    <ConfirmDialog
      :show="showBlockingConfirmation"
      :title="t('admin.promptAudit.blockingConfirm.title')"
      :message="t('admin.promptAudit.blockingConfirm.message')"
      :confirm-text="t('admin.promptAudit.blockingConfirm.confirm')"
      danger
      @confirm="confirmBlocking"
      @cancel="showBlockingConfirmation = false"
    />
    <ConfirmDialog
      :show="deleteRequest.mode !== ''"
      :title="t('admin.promptAudit.events.deleteConfirmTitle')"
      :message="t('admin.promptAudit.events.deleteConfirmMessage', { count: deleteRequest.ids.length })"
      :confirm-text="t('common.delete')"
      danger
      @confirm="confirmIDDelete"
      @cancel="clearDeleteRequest"
    />
    <FilterDeleteDialog
      :show="showFilterDelete"
      :initial-filters="filters"
      :preview="deletePreview"
      :previewing="loading.previewing"
      :deleting="loading.deleting"
      @close="closeFilterDelete"
      @preview="runFilterDeletePreview"
      @confirm="confirmFilterDelete"
      @criteria-change="clearDeletePreview"
    />
    <EventDetailDialog :show="showEventDetail" :event="activeEvent" :loading="loading.detail" @close="closeEventDetail" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'
import RuntimeOverview from './components/RuntimeOverview.vue'
import EndpointPool from './components/EndpointPool.vue'
import PolicyPanel from './components/PolicyPanel.vue'
import SafeguardPolicyPanel from './components/SafeguardPolicyPanel.vue'
import EventWorkspace from './components/EventWorkspace.vue'
import EventDetailDialog from './components/EventDetailDialog.vue'
import FilterDeleteDialog from './components/FilterDeleteDialog.vue'
import promptAuditAPI from './api'
import type {
  PromptAuditDraft,
  PromptAuditEndpointDraft,
  PromptAuditEvent,
  PromptAuditGroup,
  PromptAuditRuntime,
  PromptDeletePreview,
  PromptEventFilters,
  PromptEventPage,
  PromptLoadErrors,
  PromptProbeResult,
  PromptSafeguardPolicyPreview,
} from './types'
import {
  buildUpdateRequest,
  cloneData,
  configToDraft,
  draftFingerprint,
  emptyEventFilters,
  MAX_SAFEGUARD_POLICY_LENGTH,
  MIN_SAFEGUARD_POLICY_LENGTH,
} from './viewModel'

const { t, locale } = useI18n()
const appStore = useAppStore()
type PromptAuditSettingsTab = 'service' | 'scope' | 'policy' | 'runtime'
const settingsOpen = ref(false)
const activeSettingsTab = ref<PromptAuditSettingsTab>('service')
const settingsTabs = computed(() => [
  { id: 'service' as const, label: t('admin.promptAudit.settings.tabs.service') },
  { id: 'policy' as const, label: t('admin.promptAudit.settings.tabs.policy') },
  { id: 'scope' as const, label: t('admin.promptAudit.settings.tabs.scope') },
  { id: 'runtime' as const, label: t('admin.promptAudit.settings.tabs.runtime') },
])
const serverConfig = ref<PromptAuditDraft | null>(null)
const draft = ref<PromptAuditDraft | null>(null)
const runtime = ref<PromptAuditRuntime | null>(null)
const groups = ref<PromptAuditGroup[]>([])
const events = reactive<PromptEventPage>({ items: [], total: 0, page: 1, page_size: 20, pages: 0 })
const filters = ref<PromptEventFilters>(emptyEventFilters())
const appliedFilters = ref<PromptEventFilters>(emptyEventFilters())
const selectedEventIds = ref<number[]>([])
const activeEvent = ref<PromptAuditEvent | null>(null)
const showEventDetail = ref(false)
const probeResults = reactive<Record<string, PromptProbeResult>>({})
const probingIds = ref<string[]>([])
const safeguardPolicyPreview = ref<PromptSafeguardPolicyPreview | null>(null)
const safeguardPolicyPreviewError = ref('')
const showFilterDelete = ref(false)
const deletePreview = ref<PromptDeletePreview | null>(null)
const deletePreviewFilters = ref<PromptEventFilters | null>(null)
const showBlockingConfirmation = ref(false)
const deleteRequest = reactive<{ mode: '' | 'single' | 'batch'; ids: number[] }>({ mode: '', ids: [] })
const loading = reactive({ config: false, runtime: false, groups: false, events: false, saving: false, detail: false, deleting: false, previewing: false, policyPreview: false })
const loadErrors = reactive<PromptLoadErrors>({ config: '', runtime: '', groups: '', events: '' })
const dirty = computed(() => draftFingerprint(draft.value) !== draftFingerprint(serverConfig.value))
const hasGroqEndpoint = computed(() => draft.value?.endpoints.some((endpoint) => endpoint.protocol === 'groq_safeguard') ?? false)
const hasQwenEndpoint = computed(() => draft.value?.endpoints.some((endpoint) => endpoint.protocol === 'openai_compatible') ?? false)
const configValidationMessage = computed(() => {
  const value = draft.value
  if (!value) return ''
  if (value.worker_count < 1 || value.worker_count > 32) return t('admin.promptAudit.settings.validation.workerCount')
  if (value.queue_capacity < 1 || value.queue_capacity > 100000) return t('admin.promptAudit.settings.validation.queueCapacity')
  const policyLength = Array.from(value.groq_safeguard_policy.trim()).length
  if (policyLength > 0 && (policyLength < MIN_SAFEGUARD_POLICY_LENGTH || policyLength > MAX_SAFEGUARD_POLICY_LENGTH)) {
    return t('admin.promptAudit.settings.validation.safeguardPolicy', { min: MIN_SAFEGUARD_POLICY_LENGTH, max: MAX_SAFEGUARD_POLICY_LENGTH })
  }
  if (value.scanners.length === 0) return t('admin.promptAudit.settings.validation.scanners')
  if (!value.all_groups && value.group_ids.length === 0) return t('admin.promptAudit.settings.validation.groups')

  const endpointIDs = new Set<string>()
  for (const endpoint of value.endpoints) {
    const id = endpoint.id.trim()
    const name = endpoint.name.trim() || id || t('admin.promptAudit.pool.unnamedNode')
    if (!id || !endpoint.name.trim() || !endpoint.base_url.trim()) {
      return t('admin.promptAudit.settings.validation.endpointFields', { name })
    }
    if (endpointIDs.has(id)) return t('admin.promptAudit.settings.validation.endpointDuplicate', { id })
    endpointIDs.add(id)
    if (endpoint.timeout_ms < 100 || endpoint.timeout_ms > 30000) {
      return t('admin.promptAudit.settings.validation.endpointTimeout', { name })
    }
    if (endpoint.input_limit < 128 || endpoint.input_limit > 100000) {
      return t('admin.promptAudit.settings.validation.endpointInputLimit', { name })
    }
    if (
      value.enabled &&
      endpoint.enabled &&
      endpoint.protocol === 'groq_safeguard' &&
      !hasEndpointCredential(endpoint)
    ) {
      return t('admin.promptAudit.settings.validation.groqCredential', { name })
    }
  }
  if (value.enabled && !value.endpoints.some((endpoint) => endpoint.enabled)) {
    return t('admin.promptAudit.settings.validation.endpointRequired')
  }
  return ''
})

const SaveToggle = defineComponent({
  inheritAttrs: false,
  props: {
    label: { type: String, required: true },
    modelValue: { type: Boolean, required: true },
    disabled: { type: Boolean, default: false },
    hideLabel: { type: Boolean, default: false },
  },
  emits: ['update:modelValue'],
  setup(props, { emit, attrs }) {
    return () => h('label', { class: ['flex items-center gap-2.5 text-sm', props.disabled ? 'cursor-not-allowed opacity-50' : 'cursor-pointer'] }, [
      h('button', {
        ...attrs,
        type: 'button',
        role: 'switch',
        'aria-checked': props.modelValue,
        'aria-label': props.label,
        disabled: props.disabled,
        class: [
          'relative inline-flex h-6 w-11 shrink-0 items-center rounded-full border-2 border-transparent transition-colors duration-200 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 focus-visible:ring-offset-2',
          props.modelValue ? 'bg-primary-600' : 'bg-gray-300 dark:bg-dark-600',
          props.disabled ? 'cursor-not-allowed' : 'cursor-pointer',
        ],
        onClick: (event: MouseEvent) => {
          event.preventDefault()
          if (!props.disabled) emit('update:modelValue', !props.modelValue)
        },
      }, [
        h('span', {
          class: [
            'pointer-events-none inline-block h-5 w-5 rounded-full bg-white shadow transition-transform duration-200 ease-in-out',
            props.modelValue ? 'translate-x-5' : 'translate-x-0',
          ],
        }),
      ]),
      props.hideLabel ? null : h('span', { class: 'select-none text-gray-700 dark:text-dark-200' }, props.label),
    ])
  },
})

function hasEndpointCredential(endpoint: PromptAuditEndpointDraft): boolean {
  return Boolean(endpoint.token.trim() || (endpoint.has_token && !endpoint.clear_token))
}

function errorMessage(error: unknown, fallbackKey: string): string {
  const code = extractApiErrorCode(error)
  if (code) {
    const key = `admin.promptAudit.errors.${code}`
    const translated = t(key)
    if (translated !== key) return translated
  }
  return extractApiErrorMessage(error, t(fallbackKey))
}

async function loadConfig() {
  loading.config = true
  loadErrors.config = ''
  try {
    const config = await promptAuditAPI.getConfig()
    serverConfig.value = configToDraft(config)
    draft.value = configToDraft(config)
    clearSafeguardPolicyPreview()
  } catch (error) {
    loadErrors.config = errorMessage(error, 'admin.promptAudit.errors.loadConfig')
  } finally {
    loading.config = false
  }
}
async function loadRuntime() {
  loading.runtime = true
  loadErrors.runtime = ''
  try { runtime.value = await promptAuditAPI.getRuntime() }
  catch (error) { loadErrors.runtime = errorMessage(error, 'admin.promptAudit.errors.loadRuntime') }
  finally { loading.runtime = false }
}
async function loadGroups() {
  loading.groups = true
  loadErrors.groups = ''
  try { groups.value = await promptAuditAPI.listGroups() }
  catch (error) { loadErrors.groups = errorMessage(error, 'admin.promptAudit.errors.loadGroups') }
  finally { loading.groups = false }
}
async function loadEvents() {
  loading.events = true
  loadErrors.events = ''
  try {
    const result = await promptAuditAPI.listEvents(appliedFilters.value, events.page, events.page_size)
    Object.assign(events, result)
    selectedEventIds.value = []
  } catch (error) {
    loadErrors.events = errorMessage(error, 'admin.promptAudit.errors.loadEvents')
  } finally {
    loading.events = false
  }
}
async function loadInitial() {
  await Promise.allSettled([loadConfig(), loadRuntime(), loadGroups(), loadEvents()])
}

function replaceDraft(value: PromptAuditDraft) {
  const previous = draft.value
  if (
    !previous ||
    previous.groq_safeguard_policy !== value.groq_safeguard_policy ||
    JSON.stringify(previous.scanners) !== JSON.stringify(value.scanners)
  ) {
    clearSafeguardPolicyPreview()
  }
  draft.value = cloneData(value)
}
function updateEndpoints(value: PromptAuditEndpointDraft[]) {
  if (!draft.value) return
  replaceDraft({ ...draft.value, endpoints: value })
}
function setEnabled(value: boolean) {
  if (!draft.value) return
  replaceDraft({ ...draft.value, enabled: value, blocking_enabled: value ? draft.value.blocking_enabled : false })
}
function setBlocking(value: boolean) {
  if (!draft.value || !draft.value.enabled) return
  if (value && !draft.value.blocking_enabled) { showBlockingConfirmation.value = true; return }
  replaceDraft({ ...draft.value, blocking_enabled: value })
}
function confirmBlocking() {
  showBlockingConfirmation.value = false
  if (draft.value) replaceDraft({ ...draft.value, blocking_enabled: true })
}
function openSettings(tab: PromptAuditSettingsTab = 'service') {
  activeSettingsTab.value = tab
  settingsOpen.value = true
}
function closeSettings() {
  settingsOpen.value = false
  clearSafeguardPolicyPreview()
}
function resetDraft() {
  if (serverConfig.value) {
    draft.value = cloneData(serverConfig.value)
    clearSafeguardPolicyPreview()
  }
}
async function saveConfig() {
  if (!draft.value || !dirty.value || configValidationMessage.value) return
  loading.saving = true
  try {
    const saved = await promptAuditAPI.updateConfig(buildUpdateRequest(draft.value))
    serverConfig.value = configToDraft(saved)
    draft.value = configToDraft(saved)
    clearSafeguardPolicyPreview()
    settingsOpen.value = false
    appStore.showSuccess(t('admin.promptAudit.messages.saved'))
    await Promise.allSettled([loadRuntime(), loadEvents()])
  } catch (error) {
    const code = extractApiErrorCode(error)
    appStore.showError(errorMessage(error, code === 'prompt_audit_config_conflict' ? 'admin.promptAudit.errors.prompt_audit_config_conflict' : 'admin.promptAudit.errors.saveConfig'))
  } finally {
    loading.saving = false
  }
}
async function runProbe(endpoint: PromptAuditEndpointDraft) {
  if (probingIds.value.includes(endpoint.id)) return
  probingIds.value = [...probingIds.value, endpoint.id]
  try {
    const result = await promptAuditAPI.probeEndpoint(endpoint)
    probeResults[endpoint.id] = result
    if (result.ok) appStore.showSuccess(t('admin.promptAudit.messages.probeSucceeded'))
    else appStore.showError(`${result.error_code || result.status}: ${result.message}`)
  } catch (error) {
    appStore.showError(errorMessage(error, 'admin.promptAudit.errors.probe'))
  } finally {
    probingIds.value = probingIds.value.filter((id) => id !== endpoint.id)
  }
}

function clearSafeguardPolicyPreview() {
  safeguardPolicyPreview.value = null
  safeguardPolicyPreviewError.value = ''
}

function updateSafeguardPolicy(value: string) {
  if (!draft.value) return
  replaceDraft({ ...draft.value, groq_safeguard_policy: value })
}

async function previewSafeguardPolicy() {
  if (!draft.value || loading.policyPreview) return
  clearSafeguardPolicyPreview()
  loading.policyPreview = true
  try {
    safeguardPolicyPreview.value = await promptAuditAPI.previewSafeguardPolicy(
      draft.value.groq_safeguard_policy,
      draft.value.scanners,
    )
  } catch (error) {
    safeguardPolicyPreviewError.value = errorMessage(error, 'admin.promptAudit.errors.previewPolicy')
  } finally {
    loading.policyPreview = false
  }
}

function handleFiltersChanged(value: PromptEventFilters) {
  filters.value = cloneData(value)
  clearDeletePreview()
}
function applyEventFilters(value: PromptEventFilters) {
  filters.value = cloneData(value)
  appliedFilters.value = cloneData(value)
  events.page = 1
  clearDeletePreview()
  void loadEvents()
}
function changePage(value: number) { events.page = value; void loadEvents() }
function changePageSize(value: number) { events.page_size = value; events.page = 1; void loadEvents() }
async function openEvent(id: number) {
  showEventDetail.value = true
  loading.detail = true
  activeEvent.value = null
  try { activeEvent.value = await promptAuditAPI.getEvent(id) }
  catch (error) { appStore.showError(errorMessage(error, 'admin.promptAudit.errors.loadDetail')); showEventDetail.value = false }
  finally { loading.detail = false }
}
function closeEventDetail() { showEventDetail.value = false; activeEvent.value = null }
function requestSingleDelete(id: number) { deleteRequest.mode = 'single'; deleteRequest.ids = [id] }
function requestBatchDelete() { if (selectedEventIds.value.length) { deleteRequest.mode = 'batch'; deleteRequest.ids = [...selectedEventIds.value] } }
function clearDeleteRequest() { deleteRequest.mode = ''; deleteRequest.ids = [] }
async function confirmIDDelete() {
  const mode = deleteRequest.mode
  const ids = [...deleteRequest.ids]
  clearDeleteRequest()
  if (!mode || ids.length === 0) return
  loading.deleting = true
  try {
    const result = mode === 'single' ? await promptAuditAPI.deleteEvent(ids[0]) : await promptAuditAPI.batchDeleteEvents(ids)
    appStore.showSuccess(t('admin.promptAudit.messages.deleted', { count: result.deleted_events }))
    await Promise.allSettled([loadEvents(), loadRuntime()])
  } catch (error) { appStore.showError(errorMessage(error, 'admin.promptAudit.errors.delete')) }
  finally { loading.deleting = false }
}
function clearDeletePreview() {
  deletePreview.value = null
  deletePreviewFilters.value = null
}
function requestFilterDeletePreview() {
  clearDeletePreview()
  showFilterDelete.value = true
}
function closeFilterDelete() {
  showFilterDelete.value = false
  clearDeletePreview()
}
async function runFilterDeletePreview(value: PromptEventFilters) {
  loading.previewing = true
  try {
    deletePreview.value = await promptAuditAPI.previewDelete(value)
    deletePreviewFilters.value = cloneData(value)
  } catch (error) {
    clearDeletePreview()
    appStore.showError(errorMessage(error, 'admin.promptAudit.errors.previewDelete'))
  } finally { loading.previewing = false }
}
async function confirmFilterDelete(filters?: PromptEventFilters) {
  if (loading.deleting) return
  loading.deleting = true
  try {
    let preview = deletePreview.value
    let previewFilters = deletePreviewFilters.value ? cloneData(deletePreviewFilters.value) : null
    // One-click path: no fresh preview (never requested, or cleared by a
    // criteria change) — mint the confirmation token on the fly from the
    // criteria the dialog just emitted, then delete in the same action.
    if ((!preview || !previewFilters) && filters) {
      preview = await promptAuditAPI.previewDelete(filters)
      previewFilters = cloneData(filters)
    }
    if (!preview || !previewFilters) return
    const result = await promptAuditAPI.deleteEventsByFilter(previewFilters, preview)
    closeFilterDelete()
    appStore.showSuccess(t('admin.promptAudit.messages.deleted', { count: result.deleted_events }))
    await Promise.allSettled([loadEvents(), loadRuntime()])
  } catch (error) {
    clearDeletePreview()
    appStore.showError(errorMessage(error, 'admin.promptAudit.errors.deleteConfirmation'))
  } finally { loading.deleting = false }
}
function formatDate(value: string): string {
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'medium' }).format(new Date(value))
}

onMounted(loadInitial)
</script>
