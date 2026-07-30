import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import type { Component } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import type { DOMWrapper, VueWrapper } from '@vue/test-utils'

import RiskControlView from '../RiskControlView.vue'
import type { ContentModerationConfig, ContentModerationLog, UpdateContentModerationConfig } from '@/api/admin/riskControl'

const {
  getConfig,
  updateConfig,
  getStatus,
  listLogs,
  getCyberPolicyRequestAudit,
  getGroups,
  copyToClipboard,
  showError,
  showSuccess,
} = vi.hoisted(() => ({
  getConfig: vi.fn(),
  updateConfig: vi.fn(),
  getStatus: vi.fn(),
  listLogs: vi.fn(),
  getCyberPolicyRequestAudit: vi.fn(),
  getGroups: vi.fn(),
  copyToClipboard: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    riskControl: {
      getConfig,
      updateConfig,
      getStatus,
      listLogs,
      getCyberPolicyRequestAudit,
      testAPIKeys: vi.fn(),
      deleteFlaggedHash: vi.fn(),
      clearFlaggedHashes: vi.fn(),
      unbanUser: vi.fn(),
    },
    groups: {
      getAll: getGroups,
    },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
  }),
}))

vi.mock('@/composables/useClipboard', async () => {
  const { ref } = await import('vue')
  return {
    useClipboard: () => {
      const copied = ref(false)
      return {
        copied,
        copyToClipboard,
        resetCopied: () => {
          copied.value = false
        },
      }
    },
  }
})

vi.mock('@/utils/apiError', () => ({
  extractApiErrorMessage: (_err: unknown, fallback: string) => fallback,
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string | number>) => {
        if (key === 'admin.riskControl.preBlockAPIKeyLoadSummary') {
          return `同步并发 ${params?.active} / 可用 Key ${params?.available}，累计 ${params?.total} 次，worker：${params?.workerActive} / ${params?.workerTotal}`
        }
        return key.replace(/\{(\w+)\}/g, (_, token) => String(params?.[token] ?? `{${token}}`))
      },
    }),
  }
})

const baseConfig = (): ContentModerationConfig => ({
  enabled: true,
  mode: 'pre_block',
  base_url: 'https://api.openai.com',
  model: 'omni-moderation-latest',
  api_key_configured: false,
  api_key_masked: '',
  api_key_count: 0,
  api_key_masks: [],
  api_key_statuses: [],
  timeout_ms: 3000,
  sample_rate: 100,
  all_groups: true,
  group_ids: [],
  record_non_hits: false,
  worker_count: 4,
  queue_size: 32768,
  block_status: 403,
  block_message: '内容审计命中风险规则，请调整输入后重试',
  email_on_hit: true,
  auto_ban_enabled: true,
  ban_threshold: 10,
  violation_window_hours: 720,
  cyber_policy_exclude_from_ban_count: false,
  retry_count: 2,
  hit_retention_days: 180,
  non_hit_retention_days: 3,
  pre_hash_check_enabled: false,
  blocked_keywords: [],
  keyword_blocking_mode: 'keyword_and_api',
  thresholds: {
    harassment: 0.98,
    sexual: 0.65,
  },
  model_filter: {
    type: 'all',
    models: [],
  },
})

const runtimeStatus = () => ({
  enabled: true,
  risk_control_enabled: true,
  mode: 'pre_block',
  worker_count: 4,
  max_workers: 32,
  active_workers: 0,
  idle_workers: 4,
  queue_size: 32768,
  queue_length: 0,
  queue_usage_percent: 0,
  enqueued: 0,
  dropped: 0,
  processed: 0,
  errors: 0,
  pre_block_active: 0,
  pre_block_checked: 0,
  pre_block_allowed: 0,
  pre_block_blocked: 0,
  pre_block_errors: 0,
  pre_block_avg_latency_ms: 0,
  pre_block_api_key_active: 0,
  pre_block_api_key_available_count: 0,
  pre_block_api_key_total_calls: 0,
  pre_block_api_key_loads: [],
  api_key_statuses: [],
  flagged_hash_count: 0,
  last_cleanup_deleted_hit: 0,
  last_cleanup_deleted_non_hit: 0,
})

const moderationLog = (overrides: Partial<ContentModerationLog> = {}): ContentModerationLog => ({
  id: 1,
  request_id: 'req-1',
  user_id: 9,
  user_email: 'audit@example.com',
  api_key_id: 3,
  api_key_name: 'audit-key',
  group_id: 2,
  group_name: 'default',
  endpoint: '/v1/responses',
  provider: 'openai',
  model: 'gpt-5',
  mode: 'pre_block',
  action: 'keyword_block',
  flagged: true,
  highest_category: 'keyword',
  highest_score: 1,
  matched_keyword: 'blocked',
  category_scores: {},
  threshold_snapshot: {},
  input_excerpt: 'complete redacted input',
  upstream_latency_ms: null,
  error: '',
  violation_count: 1,
  auto_banned: false,
  email_sent: false,
  user_status: 'active',
  queue_delay_ms: null,
  cyber_request_available: false,
  created_at: '2026-07-20T12:00:00Z',
  ...overrides,
})

const AppLayoutStub = { template: '<div><slot /></div>' }
const BaseDialogStub = defineComponent({
  props: {
    show: {
      type: Boolean,
      default: false,
    },
  },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
})
const SelectStub = defineComponent({
  props: {
    modelValue: {
      type: [String, Number, Boolean],
      default: '',
    },
    options: {
      type: Array,
      default: () => [],
    },
  },
  emits: ['update:modelValue', 'change'],
  inheritAttrs: false,
  setup(props, { attrs, emit }) {
    const onChange = (event: Event) => {
      const value = (event.target as HTMLSelectElement).value
      emit('update:modelValue', value)
      emit('change', value)
    }
    return () => h(
      'select',
      {
        ...attrs,
        value: props.modelValue,
        onChange,
      },
      (props.options as Array<Record<string, unknown>>).map((option) => h(
        'option',
        {
          key: String(option.value ?? ''),
          value: option.value as string,
        },
        String(option.label ?? ''),
      )),
    )
  },
})

const PaginationStub = defineComponent({
  props: {
    page: {
      type: Number,
      default: 1,
    },
  },
  emits: ['update:page'],
  setup(_props, { emit }) {
    return () => h('button', {
      type: 'button',
      'data-test': 'next-page',
      onClick: () => emit('update:page', 2),
    }, 'next')
  },
})

const ModelWhitelistSelectorStub = defineComponent({
  props: {
    modelValue: {
      type: Array,
      default: () => [],
    },
  },
  emits: ['update:modelValue'],
  setup(props, { emit }) {
    const onInput = (event: Event) => {
      const value = (event.target as HTMLInputElement).value
      emit(
        'update:modelValue',
        value
          .split(/[,\n]/)
          .map((item) => item.trim())
          .filter(Boolean)
      )
    }
    return () =>
      h('input', {
        'data-test': 'model-filter-input',
        value: (props.modelValue as string[]).join('\n'),
        onInput,
      })
  },
})

const riskControlViewStubs = {
  AppLayout: AppLayoutStub,
  BaseDialog: BaseDialogStub,
  Icon: true,
  Select: SelectStub,
  Toggle: true,
  Pagination: PaginationStub,
  ModelWhitelistSelector: ModelWhitelistSelectorStub,
}

type RiskControlViewStubs = Partial<Record<keyof typeof riskControlViewStubs, Component | boolean>>

enableAutoUnmount(afterEach)

function mountRiskControlView(stubs: RiskControlViewStubs = {}): VueWrapper {
  return mount(RiskControlView, {
    global: {
      stubs: {
        ...riskControlViewStubs,
        ...stubs,
      },
    },
  })
}

function findButtonByText(wrapper: VueWrapper, text: string): DOMWrapper<HTMLButtonElement> {
  const button = wrapper.findAll<HTMLButtonElement>('button').find((item) => item.text().includes(text))
  if (!button) {
    throw new Error(`button not found: ${text}`)
  }
  return button
}

describe('admin RiskControlView', () => {
  beforeEach(() => {
    getConfig.mockReset()
    updateConfig.mockReset()
    getStatus.mockReset()
    listLogs.mockReset()
    getCyberPolicyRequestAudit.mockReset()
    getGroups.mockReset()
    copyToClipboard.mockReset()
    showError.mockReset()
    showSuccess.mockReset()

    getConfig.mockResolvedValue(baseConfig())
    getStatus.mockResolvedValue(runtimeStatus())
    listLogs.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 1 })
    getCyberPolicyRequestAudit.mockResolvedValue({
      log_id: 1,
      request_id: 'req-1',
      protocol: 'openai_responses',
      request_body: '{"input":"audit me"}',
      original_bytes: 20,
      stored_bytes: 20,
      truncated: false,
    })
    getGroups.mockResolvedValue([])
    copyToClipboard.mockResolvedValue(true)
    updateConfig.mockImplementation(async (payload: UpdateContentModerationConfig) => ({
      ...baseConfig(),
      ...payload,
      model_filter: payload.model_filter ?? baseConfig().model_filter,
      api_key_configured: false,
      api_key_masked: '',
      api_key_count: 0,
      api_key_masks: [],
      api_key_statuses: [],
    }))
  })

  it('saves the selected model filter mode and models', async () => {
    const wrapper = mountRiskControlView()

    await flushPromises()

    await findButtonByText(wrapper, 'admin.riskControl.openSettings').trigger('click')
    await findButtonByText(wrapper, 'admin.riskControl.tabs.scope').trigger('click')
    await findButtonByText(wrapper, 'admin.riskControl.modelFilterInclude').trigger('click')
    await wrapper.get('[data-test="model-filter-input"]').setValue('gpt-5.5, gpt-5.4')
    await findButtonByText(wrapper, 'admin.riskControl.saveConfig').trigger('click')
    await flushPromises()

    expect(updateConfig).toHaveBeenCalledWith(expect.objectContaining({
      model_filter: {
        type: 'include',
        models: ['gpt-5.5', 'gpt-5.4'],
      },
    }))
    expect(showError).not.toHaveBeenCalled()
  })

  it('submits edited risk control thresholds when saving moderation config', async () => {
    const wrapper = mountRiskControlView()

    await flushPromises()

    await findButtonByText(wrapper, 'admin.riskControl.openSettings').trigger('click')
    await findButtonByText(wrapper, 'admin.riskControl.tabs.riskThresholds').trigger('click')
    await wrapper.get('[data-test="risk-threshold-sexual"]').setValue('72')
    await wrapper.get('[data-test="risk-threshold-harassment"]').setValue('99')
    await findButtonByText(wrapper, 'admin.riskControl.saveConfig').trigger('click')
    await flushPromises()

    expect(updateConfig).toHaveBeenCalledWith(expect.objectContaining({
      thresholds: expect.objectContaining({
        sexual: 0.72,
        harassment: 0.99,
      }),
    }))
    expect(showError).not.toHaveBeenCalled()
  })

  it('describes worker runtime as async audit and pre-block record processing', async () => {
    getStatus.mockResolvedValue({
      ...runtimeStatus(),
      mode: 'observe',
      processed: 12,
      queue_length: 2,
    })

    const wrapper = mountRiskControlView()

    await flushPromises()

    expect(wrapper.text()).toContain('admin.riskControl.workerStatusHint')
    expect(wrapper.text()).not.toContain('admin.riskControl.preBlockSyncStatus')
    expect(wrapper.text()).toContain('admin.riskControl.records')
    expect(wrapper.text()).toContain('12')
    expect(wrapper.text()).toContain('2 / 32,768')
  })

  it('shows pre-block synchronous moderation metrics separately from worker queue', async () => {
    getStatus.mockResolvedValue({
      ...runtimeStatus(),
      pre_block_active: 2,
      pre_block_checked: 128,
      pre_block_allowed: 120,
      pre_block_blocked: 8,
      pre_block_errors: 1,
      pre_block_avg_latency_ms: 86,
      pre_block_api_key_active: 2,
      pre_block_api_key_available_count: 2,
      pre_block_api_key_total_calls: 128,
      active_workers: 3,
      worker_count: 7,
      pre_block_api_key_loads: [
        {
          index: 0,
          key_hash: 'hash-one',
          masked: 'sk-...one',
          status: 'ok',
          active: 1,
          total: 72,
          success: 70,
          errors: 2,
          avg_latency_ms: 84,
          last_latency_ms: 80,
          last_http_status: 200,
        },
        {
          index: 1,
          key_hash: 'hash-two',
          masked: 'sk-...two',
          status: 'ok',
          active: 1,
          total: 56,
          success: 56,
          errors: 0,
          avg_latency_ms: 90,
          last_latency_ms: 92,
          last_http_status: 200,
        },
      ],
    })

    const wrapper = mountRiskControlView()

    await flushPromises()

    expect(wrapper.text()).toContain('admin.riskControl.preBlockSyncStatus')
    expect(wrapper.text()).toContain('admin.riskControl.preBlockSyncHint')
    expect(wrapper.text()).not.toContain('admin.riskControl.workerStatus')
    expect(wrapper.text()).toContain('admin.riskControl.records')
    expect(wrapper.text()).toContain('128')
    expect(wrapper.text()).toContain('120')
    expect(wrapper.text()).toContain('8')
    expect(wrapper.text()).toContain('86 ms')
    expect(wrapper.text()).toContain('admin.riskControl.preBlockAPIKeyLoad')
    expect(wrapper.text()).toContain('sk-...one')
    expect(wrapper.text()).toContain('sk-...two')
    expect(wrapper.text()).toContain('72')
    expect(wrapper.text()).toContain('56')
    expect(wrapper.text()).toContain('同步并发 2 / 可用 Key 2，累计 128 次，worker：3 / 7')

    const runtimeCards = wrapper.get('[data-test="pre-block-runtime-cards"]')
    const syncCard = wrapper.get('[data-test="pre-block-sync-card"]')
    const apiKeyLoadCard = wrapper.get('[data-test="pre-block-api-key-load-card"]')

    expect(runtimeCards.classes()).toEqual(expect.arrayContaining([
      'grid',
      'grid-cols-1',
      'xl:grid-cols-[minmax(0,520px)_minmax(0,1fr)]',
    ]))
    expect(syncCard.element.parentElement).toBe(runtimeCards.element)
    expect(apiKeyLoadCard.element.parentElement).toBe(runtimeCards.element)
    expect(syncCard.classes()).toContain('card')
    expect(apiKeyLoadCard.classes()).toContain('card')
    expect(syncCard.get('h2').text()).toBe('admin.riskControl.preBlockSyncStatus')
    expect(syncCard.text()).toContain('admin.riskControl.preBlockSyncHint')
    expect(apiKeyLoadCard.get('h2').text()).toBe('admin.riskControl.preBlockAPIKeyLoad')
    expect(apiKeyLoadCard.text()).toContain('admin.riskControl.preBlockAPIKeyLoadHint')
    expect(wrapper.get('[data-test="pre-block-api-key-load-list"]').classes()).toEqual(expect.arrayContaining([
      'max-h-[280px]',
      'overflow-y-auto',
    ]))
  })

  it('reloads the first page with the selected action filter', async () => {
    listLogs.mockResolvedValue({
      items: [moderationLog()],
      total: 21,
      page: 1,
      page_size: 20,
      pages: 2,
    })
    const wrapper = mountRiskControlView()

    await flushPromises()
    await wrapper.get('[data-test="next-page"]').trigger('click')
    await flushPromises()
    expect(listLogs).toHaveBeenLastCalledWith(expect.objectContaining({ page: 2 }))

    await wrapper.get('[data-test="action-filter"]').setValue('keyword_block')
    await flushPromises()

    expect(listLogs).toHaveBeenLastCalledWith(expect.objectContaining({
      page: 1,
      action: 'keyword_block',
    }))
  })

  it('renders hash blocks with a block label and color', async () => {
    listLogs.mockResolvedValue({
      items: [moderationLog({ id: 88, action: 'hash_block', highest_category: 'hash' })],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const wrapper = mountRiskControlView()

    await flushPromises()

    const badge = wrapper.get('[data-test="result-badge-88"]')
    expect(badge.text()).toBe('admin.riskControl.action.hashBlock')
    expect(badge.classes()).toEqual(expect.arrayContaining(['bg-red-100', 'text-red-700']))
  })

  it('copies the complete input excerpt from a regular detail dialog', async () => {
    const completeInput = 'first line of the complete redacted input\nsecond line that must not be truncated'
    listLogs.mockResolvedValue({
      items: [moderationLog({ input_excerpt: completeInput })],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const wrapper = mountRiskControlView()

    await flushPromises()
    await findButtonByText(wrapper, 'first line of the complete redacted input').trigger('click')

    await wrapper.get('[data-test="input-detail-copy"]').trigger('click')
    await flushPromises()

    expect(copyToClipboard).toHaveBeenCalledWith(
      completeInput,
      'admin.riskControl.detailCopied',
    )
  })

  it('loads a cyber request snapshot only when its detail is opened', async () => {
    listLogs.mockResolvedValue({
      items: [moderationLog({
        id: 77,
        request_id: 'req-cyber-77',
        mode: 'post_upstream',
        action: 'cyber_policy',
        highest_category: 'cyber_policy',
        matched_keyword: '',
        input_excerpt: '',
        error: 'cyber_policy: blocked',
        cyber_request_available: true,
      })],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const completeCyberRequest = '{"model":"gpt-5","input":[{"role":"user","content":"audit request"},{"type":"function_call_output","output":"complete tool output"}],"temperature":1.23}'
    getCyberPolicyRequestAudit.mockResolvedValue({
      log_id: 77,
      request_id: 'req-cyber-77',
      protocol: 'openai_responses',
      request_body: completeCyberRequest,
      original_bytes: completeCyberRequest.length,
      stored_bytes: completeCyberRequest.length,
      truncated: false,
    })

    const wrapper = mountRiskControlView()

    await flushPromises()
    expect(getCyberPolicyRequestAudit).not.toHaveBeenCalled()

    await findButtonByText(wrapper, 'admin.riskControl.cyberRequestSaved').trigger('click')
    await flushPromises()

    expect(getCyberPolicyRequestAudit).toHaveBeenCalledWith(77)
    expect(wrapper.get('[data-test="cyber-audit-body"]').text()).toContain('complete tool output')
    expect(wrapper.get('[data-test="cyber-audit-body"]').text()).toContain('"model": "gpt-5"')
    expect(wrapper.find('[data-test="cyber-audit-truncated"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('cyber_policy: blocked')

    await wrapper.get('[data-test="cyber-audit-copy"]').trigger('click')
    expect(copyToClipboard).toHaveBeenCalledWith(
      JSON.stringify(JSON.parse(completeCyberRequest), null, 2),
      'admin.riskControl.cyberRequestCopied',
    )
  })
})
