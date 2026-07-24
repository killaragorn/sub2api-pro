import type * as VueI18n from 'vue-i18n'
import type { Account } from '@/types'

import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import AccountCapacityCell from '../AccountCapacityCell.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof VueI18n>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const CapacityBadgeStub = defineComponent({
  name: 'CapacityBadge',
  props: {
    current: { type: [Number, String], required: true },
    max: { type: [Number, String], required: true },
    suffix: { type: String, default: '' },
    tooltip: { type: String, default: '' },
    colorClass: { type: String, default: '' }
  },
  template: '<div data-testid="capacity-badge" :data-max="max" :data-suffix="suffix" :data-tooltip="tooltip" :data-color-class="colorClass" />'
})

function buildAccount(overrides: Partial<Account>): Account {
  return {
    id: 1,
    name: 'Capacity test',
    platform: 'openai',
    type: 'apikey',
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    status: 'active',
    error_message: null,
    last_used_at: null,
    expires_at: null,
    auto_pause_on_expired: false,
    created_at: '',
    updated_at: '',
    schedulable: true,
    rate_limited_at: null,
    rate_limit_reset_at: null,
    overload_until: null,
    temp_unschedulable_until: null,
    temp_unschedulable_reason: null,
    session_window_start: null,
    session_window_end: null,
    session_window_status: null,
    ...overrides
  }
}

function mountCapacity(overrides: Partial<Account>) {
  return mount(AccountCapacityCell, {
    props: { account: buildAccount(overrides) },
    global: {
      stubs: {
        CapacityBadge: CapacityBadgeStub,
        QuotaBadge: true
      }
    }
  })
}

describe('AccountCapacityCell', () => {
  it('shows deterministic OpenAI general, reserve, and total capacities', () => {
    const wrapper = mountCapacity({
      id: 1,
      platform: 'openai',
      type: 'apikey',
      concurrency: 10,
      current_concurrency: 7,
      affinity_concurrency_reserve: 3,
      general_concurrency_limit: 7,
      extra: {}
    })

    const badge = wrapper.get('[data-testid="capacity-badge"]')
    expect(badge.attributes('data-max')).toBe('G7')
    expect(badge.attributes('data-suffix')).toBe('R3 C10')
    expect(badge.attributes('data-tooltip')).toBe('admin.accounts.capacity.concurrency.affinity')
  })

  it('falls back to account extra and shows zero reserve for OpenAI', () => {
    const withReserve = mountCapacity({
      id: 2,
      platform: 'openai',
      type: 'oauth',
      concurrency: 6,
      current_concurrency: 0,
      extra: { affinity_concurrency_reserve: 2 }
    })
    expect(withReserve.get('[data-testid="capacity-badge"]').attributes('data-max')).toBe('G4')
    expect(withReserve.get('[data-testid="capacity-badge"]').attributes('data-suffix')).toBe('R2 C6')

    const withoutReserve = mountCapacity({
      id: 3,
      platform: 'openai',
      type: 'apikey',
      concurrency: 4,
      current_concurrency: 0,
      extra: {}
    })
    expect(withoutReserve.get('[data-testid="capacity-badge"]').attributes('data-max')).toBe('G4')
    expect(withoutReserve.get('[data-testid="capacity-badge"]').attributes('data-suffix')).toBe('R0 C4')
  })

  it('clamps invalid legacy reserve data to preserve one general slot', () => {
    const wrapper = mountCapacity({
      id: 6,
      platform: 'openai',
      type: 'apikey',
      concurrency: 4,
      current_concurrency: 0,
      extra: { affinity_concurrency_reserve: 9 }
    })

    const badge = wrapper.get('[data-testid="capacity-badge"]')
    expect(badge.attributes('data-max')).toBe('G1')
    expect(badge.attributes('data-suffix')).toBe('R3 C4')
  })

  it('rejects malformed legacy values and repairs an inconsistent backend split', () => {
    const wrapper = mountCapacity({
      id: 7,
      platform: 'openai',
      type: 'apikey',
      concurrency: 6,
      current_concurrency: 0,
      affinity_concurrency_reserve: -1,
      general_concurrency_limit: 99,
      extra: { affinity_concurrency_reserve: 2.5 }
    })

    const badge = wrapper.get('[data-testid="capacity-badge"]')
    expect(badge.attributes('data-max')).toBe('G6')
    expect(badge.attributes('data-suffix')).toBe('R0 C6')
  })

  it('keeps non-OpenAI capacity badges unchanged', () => {
    const wrapper = mountCapacity({
      id: 4,
      platform: 'anthropic',
      type: 'oauth',
      concurrency: 5,
      current_concurrency: 0,
      extra: {}
    })

    expect(wrapper.get('[data-testid="capacity-badge"]').attributes('data-suffix')).toBe('')
  })

  it('renders C=0 as unlimited without a full-capacity state', () => {
    const wrapper = mountCapacity({
      id: 5,
      platform: 'openai',
      type: 'apikey',
      concurrency: 0,
      current_concurrency: 12,
      affinity_concurrency_reserve: 0,
      general_concurrency_limit: 0,
      extra: {}
    })

    const badge = wrapper.get('[data-testid="capacity-badge"]')
    expect(badge.attributes('data-max')).toBe('G∞')
    expect(badge.attributes('data-suffix')).toBe('R0 C∞')
    expect(badge.attributes('data-tooltip')).toBe('admin.accounts.capacity.concurrency.unlimited')
    expect(badge.attributes('data-color-class')).not.toContain('red')
  })
})
