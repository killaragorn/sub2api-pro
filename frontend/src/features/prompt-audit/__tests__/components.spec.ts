import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import EndpointPool from '../components/EndpointPool.vue'
import PolicyPanel from '../components/PolicyPanel.vue'
import SafeguardPolicyPanel from '../components/SafeguardPolicyPanel.vue'
import RuntimeOverview from '../components/RuntimeOverview.vue'
import EventWorkspace from '../components/EventWorkspace.vue'
import EventDetailDialog from '../components/EventDetailDialog.vue'
import FilterDeleteDialog from '../components/FilterDeleteDialog.vue'
import type { PromptAuditDraft, PromptAuditEndpointDraft, PromptAuditEvent, PromptAuditRuntime, PromptEventFilters } from '../types'
import { emptyEventFilters, resolveDeleteRangeFilters, SCANNER_CATALOG } from '../viewModel'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ locale: { value: 'en' }, t: (key: string, params?: Record<string, unknown>) => key.replace(/\{(\w+)\}/g, (_, token) => String(params?.[token] ?? `{${token}}`)) }) }
})

const DialogStub = defineComponent({ props: ['show', 'title'], emits: ['close'], template: '<div v-if="show" data-test="dialog"><slot /><slot name="footer" /></div>' })
const PaginationStub = defineComponent({ props: ['total', 'page', 'pageSize'], emits: ['update:page', 'update:pageSize'], template: '<div data-test="pagination" />' })

const endpoint = (): PromptAuditEndpointDraft => ({
  id: 'guard-1', name: 'Guard One', protocol: 'openai_compatible', base_url: 'http://127.0.0.1:8000',
  model: 'guard-model', timeout_ms: 3000, input_limit: 4000, enabled: true,
  has_token: true, token_status: 'configured', token: '', clear_token: false,
})

describe('Prompt Audit components', () => {
  beforeEach(() => vi.restoreAllMocks())

  it('matches the content-audit runtime density and shows masked per-node Key load', () => {
    const runtime: PromptAuditRuntime = {
      process_status: 'running',
      effective_mode: 'blocking',
      expected_config_version: 9,
      active_config_version: 9,
      worker_total: 4,
      worker_active: 2,
      queue_capacity: 100,
      queue: { staging: 0, queued: 3, processing: 2, retry: 1, done: 40, failed: 2, active: 6 },
      processed_total: 42,
      failed_total: 2,
      enqueued_total: 48,
      dropped_total: 1,
      last_processed_at: '2026-07-28T00:00:00Z',
      database_status: 'ok',
      redis_status: 'ok',
      endpoints: {
        'groq-primary': {
          ok: true, status: 'healthy', message: 'ok', latency_ms: 18, http_status: 200,
          retryable: false, checked_at: '2026-07-28T00:00:00Z', token_applied: true,
        },
      },
      endpoint_loads: [{
        index: 0,
        endpoint_id: 'groq-primary',
        endpoint_name: 'Groq primary',
        protocol: 'groq_safeguard',
        model: 'openai/gpt-oss-safeguard-20b',
        enabled: true,
        key_configured: true,
        masked_key: 'gsk_12****cdef',
        status: 'healthy',
        active: 2,
        total: 48,
        success: 46,
        errors: 2,
        avg_latency_ms: 21,
        last_latency_ms: 18,
        last_http_status: 200,
      }],
      guard_metrics: {
        total: 42, allowed: 35, flagged: 3, blocked: 4, unavailable: 0, invalid: 0,
        timeouts: 1, failovers: 2, bulkhead_full: 0, record_failed: 0, latency_p95_ms: 32,
      },
    }

    const wrapper = mount(RuntimeOverview, {
      props: { runtime, loading: false, error: '' },
    })

    expect(wrapper.findAll('[data-test="prompt-runtime-overview-cards"] > div')).toHaveLength(4)
    expect(wrapper.find('[data-test="prompt-worker-runtime-card"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="prompt-api-key-load-card"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-test="prompt-api-key-load-row"]')).toHaveLength(1)
    expect(wrapper.get('[data-test="prompt-api-key-load-row"]').text()).toContain('gsk_12****cdef')
    expect(wrapper.get('[data-test="prompt-api-key-load-row"]').text()).toContain('openai/gpt-oss-safeguard-20b')
    expect(wrapper.get('[data-test="prompt-api-key-load-row"]').text()).not.toContain('gsk_1234567890abcdef')
    expect(wrapper.find('[data-test="prompt-guard-metrics-card"]').exists()).toBe(true)
  })

  it('edits a saved endpoint with blank-secret keep, explicit clear, replacement, and probe actions', async () => {
    const wrapper = mount(EndpointPool, {
      props: { endpoints: [endpoint()], probeResults: {}, probingIds: [] },
      global: { stubs: { BaseDialog: DialogStub } },
    })
    expect(wrapper.text()).toContain('admin.promptAudit.pool.configured')
    await wrapper.get('[aria-label="common.edit"]').trigger('click')
    const token = wrapper.get<HTMLInputElement>('[aria-label="admin.promptAudit.pool.apiKey"]')
    expect(token.element.value).toBe('')
    expect(token.attributes('placeholder')).toContain('admin.promptAudit.pool.keepSecret')

    await wrapper.get<HTMLInputElement>('[aria-label="admin.promptAudit.pool.clearSecret"]').setValue(true)
    await token.setValue('replacement-canary')
    await wrapper.get('[data-test="endpoint-editor"]').trigger('submit')
    const updated = wrapper.emitted('update:endpoints')?.at(-1)?.[0] as PromptAuditEndpointDraft[]
    expect(updated[0]).toMatchObject({ token: 'replacement-canary', clear_token: false })

    const probe = wrapper.findAll('button').find((button) => button.text().includes('admin.promptAudit.pool.probe'))
    await probe!.trigger('click')
    expect(wrapper.emitted('probe')?.[0]?.[0]).toMatchObject({ id: 'guard-1' })
  })

  it('selects the Groq Safeguard preset in the endpoint editor', async () => {
    const wrapper = mount(EndpointPool, {
      props: { endpoints: [endpoint()], probeResults: {}, probingIds: [] },
      global: { stubs: { BaseDialog: DialogStub } },
    })
    await wrapper.get('[aria-label="common.edit"]').trigger('click')
    await wrapper.get('[data-test="endpoint-protocol-groq_safeguard"]').trigger('click')
    expect(wrapper.get<HTMLInputElement>('[aria-label="admin.promptAudit.pool.baseUrl"]').element.value).toBe('https://api.groq.com/openai')
    expect(wrapper.get<HTMLInputElement>('[aria-label="admin.promptAudit.pool.model"]').element.value).toBe('openai/gpt-oss-safeguard-20b')
    expect(wrapper.get<HTMLInputElement>('[aria-label="admin.promptAudit.pool.model"]').attributes()).toHaveProperty('readonly')
    expect(wrapper.get<HTMLInputElement>('[aria-label="admin.promptAudit.pool.timeout"]').element.value).toBe('10000')
    expect(wrapper.find('[data-test="groq-safeguard-notice"]').exists()).toBe(true)
    expect(wrapper.get<HTMLInputElement>('[aria-label="admin.promptAudit.pool.apiKey"]').attributes('placeholder')).toBe('gsk_...')
    expect(wrapper.get('[data-test="endpoint-editor-error"]').text()).toBe('admin.promptAudit.pool.validation.groqCredential')

    await wrapper.get<HTMLInputElement>('[aria-label="admin.promptAudit.pool.apiKey"]').setValue('new-groq-key')

    await wrapper.get('[data-test="endpoint-editor"]').trigger('submit')
    const updated = wrapper.emitted('update:endpoints')?.at(-1)?.[0] as PromptAuditEndpointDraft[]
    expect(updated[0]).toMatchObject({
      protocol: 'groq_safeguard',
      base_url: 'https://api.groq.com/openai',
      model: 'openai/gpt-oss-safeguard-20b',
      timeout_ms: 10000,
      token: 'new-groq-key',
      clear_token: false,
    })
  })

  it('requires a credential for enabled Groq nodes and allows probing once supplied', async () => {
    const missingCredential = { ...endpoint(), protocol: 'groq_safeguard' as const, model: 'openai/gpt-oss-safeguard-20b', has_token: false, token_status: 'missing' }
    const wrapper = mount(EndpointPool, {
      props: { endpoints: [missingCredential], probeResults: {}, probingIds: [] },
      global: { stubs: { BaseDialog: DialogStub } },
    })
    expect(wrapper.text()).toContain('admin.promptAudit.pool.credentialRequired')
    expect(wrapper.findAll('button').find((button) => button.text().includes('admin.promptAudit.pool.probe'))?.attributes()).toHaveProperty('disabled')

    await wrapper.get('[aria-label="common.edit"]').trigger('click')
    expect(wrapper.get('[data-test="endpoint-editor-error"]').text()).toBe('admin.promptAudit.pool.validation.groqCredential')
    expect(wrapper.get('[data-test="save-endpoint"]').attributes()).toHaveProperty('disabled')
    await wrapper.get<HTMLInputElement>('[aria-label="admin.promptAudit.pool.apiKey"]').setValue('temporary-groq-key')
    expect(wrapper.find('[data-test="endpoint-editor-error"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="probe-endpoint-editor"]').attributes()).not.toHaveProperty('disabled')
  })

  it('keeps credentials optional for enabled Qwen-compatible nodes', async () => {
    const missingCredential = { ...endpoint(), has_token: false, token_status: 'missing' }
    const wrapper = mount(EndpointPool, {
      props: { endpoints: [missingCredential], probeResults: {}, probingIds: [] },
      global: { stubs: { BaseDialog: DialogStub } },
    })

    expect(wrapper.text()).toContain('admin.promptAudit.pool.credentialOptional')
    await wrapper.get('[aria-label="common.edit"]').trigger('click')
    expect(wrapper.find('[data-test="endpoint-editor-error"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="save-endpoint"]').attributes()).not.toHaveProperty('disabled')
    expect(wrapper.get('[data-test="probe-endpoint-editor"]').attributes()).not.toHaveProperty('disabled')
  })

  it('supports group search, stale configured groups, nine scanners, and bounded worker inputs', async () => {
    const draft: PromptAuditDraft = {
      enabled: true, blocking_enabled: false, store_pass_events: false, effective_mode: 'async_audit', strategy: 'priority',
      groq_safeguard_policy: 'Default safeguard classification policy with enough detail for validation.',
      groq_safeguard_default_policy: 'Default safeguard classification policy with enough detail for validation.',
      worker_count: 4, queue_capacity: 100, scanners: SCANNER_CATALOG.map((item) => item.id), all_groups: false, group_ids: [1, 99],
      endpoints: [endpoint()], config_version: 1, updated_at: '', updated_by: 0, change_summary: '',
    }
    const groups = [{ id: 1, name: 'Alpha', platform: 'openai', status: 'active' }, { id: 2, name: 'Beta', platform: 'claude', status: 'inactive' }]
    const scope = mount(PolicyPanel, {
      props: { section: 'scope', draft, groups },
    })
    expect(scope.text()).toContain('99')
    expect(scope.findAll('input[type="checkbox"]').filter((input) => SCANNER_CATALOG.some((scanner) => input.attributes('aria-label') === `admin.promptAudit.scanners.${scanner.id}`))).toHaveLength(9)
    await scope.get('[aria-label="admin.promptAudit.policy.searchGroups"]').setValue('Beta')
    expect(scope.text()).toContain('Beta')
    expect(scope.text()).not.toContain('Alpha')

    const runtime = mount(PolicyPanel, { props: { section: 'runtime', draft, groups } })
    await runtime.get('[aria-label="admin.promptAudit.policy.workerCount"]').setValue('6')
    const emitted = runtime.emitted('update:draft')?.at(-1)?.[0] as PromptAuditDraft
    expect(emitted.worker_count).toBe(6)
  })

  it('edits, restores, and previews the shared Groq Safeguard classification policy', async () => {
    const defaultPolicy = 'Default safeguard classification policy with enough detail for validation.'
    const customPolicy = 'Custom safeguard classification policy with enough detail for validation.'
    const wrapper = mount(SafeguardPolicyPanel, {
      props: {
        policy: customPolicy,
        defaultPolicy,
        scanners: ['pii', 'jailbreak'],
        hasGroq: true,
        hasQwen: true,
        preview: null,
        previewing: false,
        previewError: '',
      },
    })

    expect(wrapper.find('[data-test="safeguard-policy-editor"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="qwen-builtin-policy-note"]').exists()).toBe(true)
    await wrapper.get<HTMLTextAreaElement>('[data-test="safeguard-policy-editor"]').setValue('Another valid custom safeguard classification policy body.')
    expect(wrapper.emitted('update:policy')?.at(-1)?.[0]).toContain('Another valid custom')
    await wrapper.get('[data-test="restore-safeguard-policy"]').trigger('click')
    expect(wrapper.emitted('update:policy')?.at(-1)?.[0]).toBe(defaultPolicy)
    await wrapper.get('[data-test="preview-safeguard-policy"]').trigger('click')
    expect(wrapper.emitted('preview')).toHaveLength(1)

    await wrapper.setProps({
      preview: {
        policy: customPolicy,
        prompt: 'backend-rendered final system prompt',
        policy_character_count: customPolicy.length,
        prompt_character_count: 36,
        using_default: false,
      },
    })
    expect(wrapper.get('[data-test="safeguard-policy-preview"]').text()).toContain('backend-rendered final system prompt')
  })

  it('shows Qwen3Guard model policy without exposing a prompt editor', () => {
    const wrapper = mount(SafeguardPolicyPanel, {
      props: {
        policy: '', defaultPolicy: '', scanners: ['pii'], hasGroq: false, hasQwen: true,
        preview: null, previewing: false, previewError: '',
      },
    })
    expect(wrapper.find('[data-test="qwen-builtin-policy-only"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="safeguard-policy-editor"]').exists()).toBe(false)
  })

  it('keeps identity fields separate, supports selection, and opens filter deletion from the toolbar', async () => {
    const event: PromptAuditEvent = {
      id: 1, job_id: 1, decision: 'critical', risk_level: 'critical', action: 'Block', categories: ['pii'], matched_scanners: ['pii'], scanner_scores: { pii: 1 }, scanner_evidence: { pii: 'redacted' }, scanner_backend: 'qwen3guard-openai', scanner_version: '1', guard_endpoint_id: 'guard-1', policy_id: 'priority', policy_version: 1, config_version: 1, chunk_total: 1, latency_ms: 10, issue_summaries: [], created_at: '2026-07-16T00:00:00Z',
      snapshot: { request_id: 'req-1', user_id: 1, username: 'alice', user_email: 'alice@example.test', api_key_id: 2, api_key_name: 'alice-key', group_id: 3, group_name: 'Alpha', provider: 'openai', endpoint: '/v1/chat/completions', protocol: 'openai_chat', model: 'gpt-test', prompt_hash: 'a'.repeat(64), redacted_preview: 'redacted preview', full_prompt: 'full prompt text', prompt_length: 10, message_count: 1, stage: 'http' },
    }
    const wrapper = mount(EventWorkspace, {
      props: { events: [event], total: 1, page: 1, pageSize: 20, filters: emptyEventFilters(), selectedIds: [], loading: false, error: '' },
      global: { stubs: { Pagination: PaginationStub } },
    })
    expect(wrapper.text()).toContain('alice')
    expect(wrapper.text()).toContain('alice@example.test')
    expect(wrapper.text()).toContain('alice-key')
    expect(wrapper.text()).toContain('admin.promptAudit.decisions.critical · admin.promptAudit.riskLevels.critical')
    expect(wrapper.text()).toContain('admin.promptAudit.scanners.pii')
    expect(wrapper.get('[data-test="filter-delete"]').attributes()).not.toHaveProperty('disabled')
    await wrapper.get('[data-test="filter-delete"]').trigger('click')
    expect(wrapper.emitted('preview-delete')).toHaveLength(1)
    await wrapper.get('[aria-label="admin.promptAudit.events.selectEvent"]').setValue(true)
    expect(wrapper.emitted('selection')?.at(-1)?.[0]).toEqual([1])
  })

  it('resolves delete range presets to an epoch start and a cutoff end', () => {
    const now = Date.parse('2026-07-17T12:00:00.000Z')
    const sevenDays = resolveDeleteRangeFilters(emptyEventFilters(), '7d', now)
    expect(sevenDays.start_at).toBe('1970-01-01T00:00:00.000Z')
    expect(sevenDays.end_at).toBe('2026-07-10T12:00:00.000Z')
    const all = resolveDeleteRangeFilters(emptyEventFilters(), 'all', now)
    expect(all.start_at).toBe('1970-01-01T00:00:00.000Z')
    expect(all.end_at).toBe('2026-07-17T12:00:00.000Z')
    const customSource = { ...emptyEventFilters(), start_at: '2026-07-01T00:00', end_at: '2026-07-02T00:00' }
    expect(resolveDeleteRangeFilters(customSource, 'custom', now)).toEqual(customSource)
  })

  it('drives filter deletion through presets, custom validation, preview, and confirm', async () => {
    const wrapper = mount(FilterDeleteDialog, {
      props: { show: true, initialFilters: emptyEventFilters(), preview: null, previewing: false, deleting: false },
      global: { stubs: { BaseDialog: DialogStub } },
    })
    expect(wrapper.get<HTMLInputElement>('[data-test="range-preset-7d"]').element.checked).toBe(true)
    expect(wrapper.find('[data-test="custom-range"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="delete-preview-empty"]').exists()).toBeTruthy()
    // A valid preset is enough: confirm is armed immediately (one-click flow)
    // and needs no disabled-reason hint.
    expect(wrapper.get('[data-test="confirm-filter-delete"]').attributes()).not.toHaveProperty('disabled')
    expect(wrapper.find('[data-test="confirm-disabled-reason"]').exists()).toBe(false)
    await wrapper.get('[data-test="confirm-filter-delete"]').trigger('click')
    const directConfirm = wrapper.emitted('confirm')?.at(-1)?.[0] as PromptEventFilters
    expect(directConfirm.start_at).toBe('1970-01-01T00:00:00.000Z')
    expect(Date.now() - new Date(directConfirm.end_at).getTime()).toBeGreaterThanOrEqual(7 * 24 * 60 * 60 * 1000)

    await wrapper.get('[data-test="range-preset-30d"]').setValue()
    expect(wrapper.emitted('criteria-change')?.length).toBeGreaterThan(0)
    await wrapper.get('[data-test="delete-risk"]').setValue('high')
    await wrapper.get('[data-test="run-delete-preview"]').trigger('click')
    const presetPreview = wrapper.emitted('preview')?.at(-1)?.[0] as PromptEventFilters
    expect(presetPreview.risk_level).toBe('high')
    expect(presetPreview.start_at).toBe('1970-01-01T00:00:00.000Z')
    expect(Date.now() - new Date(presetPreview.end_at).getTime()).toBeGreaterThanOrEqual(30 * 24 * 60 * 60 * 1000)

    await wrapper.get('[data-test="range-preset-custom"]').setValue()
    expect(wrapper.find('[data-test="custom-range"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="run-delete-preview"]').attributes()).toHaveProperty('disabled')
    expect(wrapper.get('[data-test="confirm-filter-delete"]').attributes()).toHaveProperty('disabled')
    expect(wrapper.get('[data-test="confirm-disabled-reason"]').text()).toBe('admin.promptAudit.events.filterDeleteConfirmInvalidRange')
    expect(wrapper.get('[data-test="confirm-filter-delete"]').attributes('title')).toBe('admin.promptAudit.events.filterDeleteConfirmInvalidRange')
    await wrapper.get('[data-test="custom-range"] [aria-label="admin.promptAudit.events.startAt"]').setValue('2026-07-01T00:00')
    await wrapper.get('[data-test="custom-range"] [aria-label="admin.promptAudit.events.endAt"]').setValue('2026-07-02T00:00')
    expect(wrapper.get('[data-test="run-delete-preview"]').attributes()).not.toHaveProperty('disabled')
    expect(wrapper.get('[data-test="confirm-filter-delete"]').attributes()).not.toHaveProperty('disabled')
    expect(wrapper.find('[data-test="confirm-disabled-reason"]').exists()).toBe(false)
    await wrapper.get('[data-test="run-delete-preview"]').trigger('click')
    const customPreview = wrapper.emitted('preview')?.at(-1)?.[0] as PromptEventFilters
    expect(customPreview.start_at).toBe('2026-07-01T00:00')
    expect(customPreview.end_at).toBe('2026-07-02T00:00')

    await wrapper.setProps({
      preview: { matched_count: 3, filter_summary: {}, snapshot_max_id: 9, filter_hash: 'b'.repeat(64), confirmation_token: 'tok', expires_at: '2026-07-16T00:05:00Z' },
    })
    expect(wrapper.get('[data-test="delete-preview-result"]').text()).toContain('admin.promptAudit.events.filterDeleteCount')
    expect(wrapper.find('[data-test="confirm-disabled-reason"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="confirm-filter-delete"]').attributes()).not.toHaveProperty('disabled')
    await wrapper.get('[data-test="confirm-filter-delete"]').trigger('click')
    const confirmed = wrapper.emitted('confirm')?.at(-1)?.[0] as PromptEventFilters
    expect(confirmed.start_at).toBe('2026-07-01T00:00')
    expect(confirmed.end_at).toBe('2026-07-02T00:00')
  })

  it('explains that a zero-match preview leaves nothing to delete', async () => {
    const wrapper = mount(FilterDeleteDialog, {
      props: {
        show: true,
        initialFilters: emptyEventFilters(),
        preview: { matched_count: 0, filter_summary: {}, snapshot_max_id: 0, filter_hash: 'c'.repeat(64), confirmation_token: 'tok', expires_at: '2026-07-16T00:05:00Z' },
        previewing: false,
        deleting: false,
      },
      global: { stubs: { BaseDialog: DialogStub } },
    })
    expect(wrapper.get('[data-test="confirm-filter-delete"]').attributes()).toHaveProperty('disabled')
    expect(wrapper.get('[data-test="confirm-disabled-reason"]').text()).toBe('admin.promptAudit.events.filterDeleteConfirmNoMatches')
    await wrapper.setProps({ previewing: true })
    expect(wrapper.find('[data-test="confirm-disabled-reason"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="confirm-filter-delete"]').attributes()).toHaveProperty('disabled')
  })

  it('inherits an explicit list-filter range as the custom preset', async () => {
    const initialFilters = { ...emptyEventFilters(), start_at: '2026-07-01T00:00', end_at: '2026-07-02T00:00', decision: 'critical' }
    const wrapper = mount(FilterDeleteDialog, {
      props: { show: true, initialFilters, preview: null, previewing: false, deleting: false },
      global: { stubs: { BaseDialog: DialogStub } },
    })
    expect(wrapper.get<HTMLInputElement>('[data-test="range-preset-custom"]').element.checked).toBe(true)
    expect(wrapper.get<HTMLInputElement>('[data-test="custom-range"] [aria-label="admin.promptAudit.events.startAt"]').element.value).toBe('2026-07-01T00:00')
    expect(wrapper.get<HTMLSelectElement>('[data-test="delete-decision"]').element.value).toBe('critical')
    expect(wrapper.get('[data-test="run-delete-preview"]').attributes()).not.toHaveProperty('disabled')
  })

  it('shows the full unredacted prompt and structured guard return on the risks tab', async () => {
    const event: PromptAuditEvent = {
      id: 1, job_id: 1, decision: 'critical', risk_level: 'critical', action: 'Block',
      categories: ['sexual_content_or_sexual_acts'], matched_scanners: ['sexual_content_or_sexual_acts'],
      scanner_scores: { sexual_content_or_sexual_acts: 1 },
      scanner_evidence: { sexual_content_or_sexual_acts: 'Sexual Content or Sexual Acts' },
      scanner_backend: 'qwen3guard-openai', scanner_version: 'qwen3guard', guard_endpoint_id: 'guard-1',
      policy_id: 'priority', policy_version: 1, config_version: 1, chunk_total: 1, latency_ms: 12,
      issue_summaries: [{
        category: 'sexual_content_or_sexual_acts', scanner_id: 'sexual_content_or_sexual_acts',
        title: '性内容或性行为', description: 'Sexual content or sexual acts', severity: 'critical',
        severity_label: '严重', action: 'Block', action_label: '阻止',
        code: 'prompt_audit_sexual_content_or_sexual_acts', score: 1,
        evidence: 'Sexual Content or Sexual Acts', evidence_hash: 'abc',
      }],
      created_at: '2026-07-16T00:00:00Z',
      snapshot: {
        request_id: 'req-1', user_id: 1, username: 'alice', user_email: 'alice@example.test',
        api_key_id: 2, api_key_name: 'alice-key', group_id: 3, group_name: 'Alpha', provider: 'openai',
        endpoint: '/v1/chat/completions', protocol: 'openai_chat', model: 'gpt-test',
        prompt_hash: 'a'.repeat(64), redacted_preview: 'redacted prompt body', full_prompt: 'complete unmasked prompt body', prompt_length: 20,
        message_count: 1, stage: 'http',
      },
    }
    const wrapper = mount(EventDetailDialog, {
      props: { show: true, event, loading: false },
      global: { stubs: { BaseDialog: DialogStub } },
    })
    const panel = wrapper.get('[data-test="event-detail-tab-panel"]')
    expect(panel.classes()).toContain('h-[min(62vh,36rem)]')
    expect(panel.classes()).toContain('overflow-y-auto')

    const riskTab = wrapper.findAll('[role="tab"]').find((tab) => tab.text().includes('admin.promptAudit.events.tabs.risks'))
    expect(riskTab).toBeTruthy()
    await riskTab!.trigger('click')
    expect(wrapper.get('[data-test="event-detail-tab-panel"]').classes()).toContain('h-[min(62vh,36rem)]')
    expect(wrapper.get('[data-test="risk-prompt-preview"]').text()).toContain('complete unmasked prompt body')
    expect(wrapper.get('[data-test="risk-prompt-preview"]').text()).not.toContain('redacted prompt body')
    expect(wrapper.get('[data-test="risk-prompt-full"]').classes()).toContain('overflow-auto')
    expect(wrapper.get('[data-test="risk-guard-return"]').text()).toContain('"decision": "admin.promptAudit.decisions.critical"')
    expect(wrapper.get('[data-test="risk-guard-return"]').text()).toContain('admin.promptAudit.scanners.sexual_content_or_sexual_acts')
    expect(wrapper.get('[data-test="risk-issue"]').text()).toContain('admin.promptAudit.scanners.sexual_content_or_sexual_acts')
  })

  it('falls back to the redacted preview for events stored before full prompts were kept', async () => {
    const event: PromptAuditEvent = {
      id: 2, job_id: 2, decision: 'flag', risk_level: 'medium', action: 'Warn',
      categories: ['pii'], matched_scanners: ['pii'], scanner_scores: {}, scanner_evidence: {},
      scanner_backend: 'qwen3guard-openai', scanner_version: '1', guard_endpoint_id: 'guard-1',
      policy_id: 'priority', policy_version: 1, config_version: 1, chunk_total: 1, latency_ms: 5,
      issue_summaries: [], created_at: '2026-07-16T00:00:00Z',
      snapshot: {
        request_id: 'req-2', user_id: 1, username: 'bob', user_email: '', api_key_id: 2,
        api_key_name: 'bob-key', group_id: 3, group_name: 'Alpha', provider: 'openai',
        endpoint: '/v1/chat/completions', protocol: 'openai_chat', model: 'gpt-test',
        prompt_hash: 'b'.repeat(64), redacted_preview: 'legacy redacted preview', full_prompt: '', prompt_length: 20,
        message_count: 1, stage: 'http',
      },
    }
    const wrapper = mount(EventDetailDialog, {
      props: { show: true, event, loading: false },
      global: { stubs: { BaseDialog: DialogStub } },
    })
    const riskTab = wrapper.findAll('[role="tab"]').find((tab) => tab.text().includes('admin.promptAudit.events.tabs.risks'))
    await riskTab!.trigger('click')
    expect(wrapper.get('[data-test="risk-prompt-full"]').text()).toContain('legacy redacted preview')
  })
})
