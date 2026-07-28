import type {
  PromptAuditConfig,
  PromptAuditDraft,
  PromptAuditEndpointDraft,
  PromptAuditEndpointProtocol,
  PromptAuditUpdateRequest,
  PromptEventFilters,
} from './types'

export const DEFAULT_GUARD_MODEL = 'sileader/qwen3guard:0.6b'
export const DEFAULT_GROQ_SAFEGUARD_MODEL = 'openai/gpt-oss-safeguard-20b'
export const DEFAULT_GROQ_TPM_LIMIT = 8000
export const MIN_GROQ_TPM_LIMIT = 100
export const MAX_GROQ_TPM_LIMIT = 10000000
export const MIN_SAFEGUARD_POLICY_LENGTH = 32
export const MAX_SAFEGUARD_POLICY_LENGTH = 16000
export const RECOMMENDED_SAFEGUARD_POLICY_MIN_LENGTH = 1200
export const RECOMMENDED_SAFEGUARD_POLICY_MAX_LENGTH = 4000

export const PROMPT_AUDIT_PROTOCOLS: ReadonlyArray<{
  id: PromptAuditEndpointProtocol
  baseUrl: string
  model: string
  timeoutMs: number
  inputLimit: number
  tpmLimit: number
}> = [
  {
    id: 'openai_compatible',
    baseUrl: 'http://127.0.0.1:8000',
    model: DEFAULT_GUARD_MODEL,
    timeoutMs: 3000,
    inputLimit: 4000,
    tpmLimit: 0,
  },
  {
    id: 'groq_safeguard',
    baseUrl: 'https://api.groq.com/openai',
    model: DEFAULT_GROQ_SAFEGUARD_MODEL,
    timeoutMs: 10000,
    inputLimit: 4000,
    tpmLimit: DEFAULT_GROQ_TPM_LIMIT,
  },
]

export const SCANNER_CATALOG = [
  { id: 'violent', label: 'Violent' },
  { id: 'non_violent_illegal_acts', label: 'Non-violent Illegal Acts' },
  { id: 'sexual_content_or_sexual_acts', label: 'Sexual Content or Sexual Acts' },
  { id: 'pii', label: 'PII' },
  { id: 'suicide_and_self_harm', label: 'Suicide & Self-Harm' },
  { id: 'unethical_acts', label: 'Unethical Acts' },
  { id: 'politically_sensitive_topics', label: 'Politically Sensitive Topics' },
  { id: 'copyright_violation', label: 'Copyright Violation' },
  { id: 'jailbreak', label: 'Jailbreak' },
] as const

// Vue props/refs are proxies and cannot be passed to structuredClone in every
// browser. Prompt Audit state is JSON-only, so this produces a detached draft
// without retaining reactive proxies or browser storage references.
export function cloneData<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}

export function configToDraft(config: PromptAuditConfig): PromptAuditDraft {
  const defaultPolicy = config.groq_safeguard_default_policy ?? ''
  return {
    ...cloneData(config),
    groq_safeguard_policy: config.groq_safeguard_policy?.trim() || defaultPolicy,
    groq_safeguard_default_policy: defaultPolicy,
    group_ids: [...(config.group_ids ?? [])],
    scanners: [...(config.scanners ?? [])],
    endpoints: (config.endpoints ?? []).map((endpoint) => ({
      ...endpoint,
      model: endpoint.protocol === 'groq_safeguard' ? DEFAULT_GROQ_SAFEGUARD_MODEL : endpoint.model,
      tpm_limit: endpoint.protocol === 'groq_safeguard'
        ? endpoint.tpm_limit || DEFAULT_GROQ_TPM_LIMIT
        : 0,
      token: '',
      clear_token: false,
    })),
  }
}

export function createDefaultEndpoint(index = 1): PromptAuditEndpointDraft {
  return {
    id: `guard-${Date.now()}-${index}`,
    name: `Guard ${index}`,
    protocol: 'openai_compatible',
    base_url: 'http://127.0.0.1:8000',
    model: DEFAULT_GUARD_MODEL,
    timeout_ms: 3000,
    input_limit: 4000,
    tpm_limit: 0,
    enabled: true,
    has_token: false,
    token_status: 'missing',
    token: '',
    clear_token: false,
  }
}

export function applyEndpointProtocolPreset(
  endpoint: PromptAuditEndpointDraft,
  protocol: PromptAuditEndpointProtocol,
): PromptAuditEndpointDraft {
  const preset = PROMPT_AUDIT_PROTOCOLS.find((item) => item.id === protocol)
  if (!preset) return { ...cloneData(endpoint), protocol }
  return {
    ...cloneData(endpoint),
    protocol,
    base_url: preset.baseUrl,
    model: preset.model,
    timeout_ms: preset.timeoutMs,
    input_limit: preset.inputLimit,
    tpm_limit: preset.tpmLimit,
  }
}


export function buildUpdateRequest(draft: PromptAuditDraft): PromptAuditUpdateRequest {
  return {
    expected_config_version: draft.config_version,
    enabled: draft.enabled,
    blocking_enabled: draft.enabled && draft.blocking_enabled,
    store_pass_events: draft.store_pass_events,
    groq_safeguard_policy: draft.groq_safeguard_policy.trim(),
    strategy: 'priority',
    worker_count: Number(draft.worker_count),
    queue_capacity: Number(draft.queue_capacity),
    scanners: [...draft.scanners],
    all_groups: draft.all_groups,
    group_ids: draft.all_groups ? [] : [...draft.group_ids].sort((a, b) => a - b),
    endpoints: draft.endpoints.map((endpoint) => ({
      id: endpoint.id.trim(),
      name: endpoint.name.trim(),
      protocol: endpoint.protocol,
      base_url: endpoint.base_url.trim(),
      model: endpoint.protocol === 'groq_safeguard'
        ? DEFAULT_GROQ_SAFEGUARD_MODEL
        : endpoint.model.trim() || DEFAULT_GUARD_MODEL,
      token: endpoint.token.trim() || undefined,
      clear_token: endpoint.clear_token,
      timeout_ms: Number(endpoint.timeout_ms),
      input_limit: Number(endpoint.input_limit),
      tpm_limit: endpoint.protocol === 'groq_safeguard'
        ? Number(endpoint.tpm_limit || DEFAULT_GROQ_TPM_LIMIT)
        : 0,
      enabled: endpoint.enabled,
    })),
  }
}

export function draftFingerprint(draft: PromptAuditDraft | null): string {
  if (!draft) return ''
  return JSON.stringify(buildUpdateRequest(draft))
}

export function emptyEventFilters(): PromptEventFilters {
  return {
    decision: '',
    risk_level: '',
    endpoint: '',
    group_id: '',
    user_id: '',
    api_key_id: '',
    request_id: '',
    prompt_hash: '',
    keyword: '',
    start_at: '',
    end_at: '',
  }
}

function toISO(value: string): string | undefined {
  if (!value.trim()) return undefined
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? undefined : date.toISOString()
}

export function eventQueryParams(filters: PromptEventFilters): Record<string, string | number> {
  const result: Record<string, string | number> = {}
  for (const key of ['decision', 'risk_level', 'endpoint', 'request_id', 'prompt_hash', 'keyword'] as const) {
    const value = filters[key].trim()
    if (value) result[key] = value
  }
  for (const key of ['group_id', 'user_id', 'api_key_id'] as const) {
    const value = Number(filters[key])
    if (Number.isInteger(value) && value > 0) result[key] = value
  }
  const start = toISO(filters.start_at)
  const end = toISO(filters.end_at)
  if (start) result.start_at = start
  if (end) result.end_at = end
  return result
}

export function eventFilterPayload(filters: PromptEventFilters): Record<string, unknown> {
  return eventQueryParams(filters)
}

export function hasExplicitDeleteRange(filters: PromptEventFilters): boolean {
  const start = toISO(filters.start_at)
  const end = toISO(filters.end_at)
  return Boolean(start && end && new Date(start).getTime() < new Date(end).getTime())
}

export type DeleteRangePreset = '1d' | '7d' | '30d' | '90d' | 'all' | 'custom'

export const DELETE_RANGE_PRESETS: ReadonlyArray<{ id: DeleteRangePreset; days: number | null }> = [
  { id: '1d', days: 1 },
  { id: '7d', days: 7 },
  { id: '30d', days: 30 },
  { id: '90d', days: 90 },
  { id: 'all', days: null },
  { id: 'custom', days: null },
]

const DAY_MS = 24 * 60 * 60 * 1000

// Presets delete events older than the chosen cutoff: the range always starts
// at the epoch and ends at (now - days) so the backend's explicit-range
// requirement is satisfied without asking the user for a begin date.
export function resolveDeleteRangeFilters(
  filters: PromptEventFilters,
  preset: DeleteRangePreset,
  now: number = Date.now(),
): PromptEventFilters {
  const resolved = cloneData(filters)
  if (preset === 'custom') return resolved
  const days = DELETE_RANGE_PRESETS.find((item) => item.id === preset)?.days ?? null
  resolved.start_at = new Date(0).toISOString()
  resolved.end_at = new Date(days === null ? now : now - days * DAY_MS).toISOString()
  return resolved
}
