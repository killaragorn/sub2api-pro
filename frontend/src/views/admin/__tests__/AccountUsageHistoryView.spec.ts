import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import AccountUsageHistoryView from '@/views/admin/AccountUsageHistoryView.vue'
import type { AccountUsageHistoryResponse } from '@/types'

const { getById, getUsageHistory, showError, push } = vi.hoisted(() => ({
  getById: vi.fn(),
  getUsageHistory: vi.fn(),
  showError: vi.fn(),
  push: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getById,
      getUsageHistory
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError })
}))

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { id: '42' } }),
  useRouter: () => ({ push })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const AppLayoutStub = defineComponent({
  template: '<main><slot /></main>'
})

const dayResponse: AccountUsageHistoryResponse = {
  items: [
    {
      period_start: '2026-08-01',
      period_end: '2026-08-01',
      requests: 12,
      tokens: 3456,
      standard_cost: 2,
      account_cost: 1.25,
      user_cost: 2.5
    }
  ],
  summary: {
    total_periods: 7,
    total_requests: 99,
    total_tokens: 123456,
    total_standard_cost: 20,
    total_account_cost: 12.5,
    total_user_cost: 25,
    first_period_start: '2026-07-20',
    last_period_end: '2026-08-01'
  },
  total: 7,
  page: 1,
  page_size: 50,
  pages: 1,
  granularity: 'day',
  timezone: 'Asia/Taipei'
}

const weekResponse: AccountUsageHistoryResponse = {
  ...dayResponse,
  items: [
    {
      ...dayResponse.items[0],
      period_start: '2026-07-27',
      period_end: '2026-08-02',
      account_cost: 7.5
    }
  ],
  summary: {
    ...dayResponse.summary,
    total_periods: 2,
    first_period_start: '2026-07-20',
    last_period_end: '2026-08-02'
  },
  total: 2,
  granularity: 'week'
}

function mountView() {
  return mount(AccountUsageHistoryView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        Icon: true,
        LoadingSpinner: true,
        Pagination: true
      }
    }
  })
}

describe('AccountUsageHistoryView', () => {
  beforeEach(() => {
    getById.mockReset()
    getUsageHistory.mockReset()
    showError.mockReset()
    push.mockReset()
    getById.mockResolvedValue({ id: 42, name: 'Account 42', status: 'active' })
    getUsageHistory.mockResolvedValueOnce(dayResponse).mockResolvedValueOnce(weekResponse)
  })

  it('renders daily totals and reloads grouped weekly totals without charts', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(getById).toHaveBeenCalledWith(42)
    expect(getUsageHistory).toHaveBeenNthCalledWith(1, 42, {
      granularity: 'day',
      page: 1,
      page_size: 50
    })
    expect(wrapper.text()).toContain('2026-08-01')
    expect(wrapper.text()).toContain('1.25')
    expect(wrapper.find('canvas').exists()).toBe(false)

    await wrapper.get('[data-testid="granularity-week"]').trigger('click')
    await flushPromises()

    expect(getUsageHistory).toHaveBeenNthCalledWith(2, 42, {
      granularity: 'week',
      page: 1,
      page_size: 50
    })
    expect(wrapper.text()).toContain('2026-07-27 – 2026-08-02')
    expect(wrapper.text()).toContain('7.5')
    wrapper.unmount()
  })
})
