import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import AccountStatsModal from './AccountStatsModal.vue'
import type { Account } from '@/types'

const { resolve } = vi.hoisted(() => ({
  resolve: vi.fn(() => ({ href: '/admin/accounts/42/usage-history' }))
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ resolve })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getStats: vi.fn()
    }
  }
}))

const BaseDialogStub = defineComponent({
  props: {
    show: { type: Boolean, default: false }
  },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

describe('AccountStatsModal history entry', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    resolve.mockClear()
  })

  it('opens the complete account history route in a new tab', async () => {
    const open = vi.spyOn(window, 'open').mockImplementation(() => null)
    const account = {
      id: 42,
      name: 'Account 42',
      status: 'active'
    } as unknown as Account

    const wrapper = mount(AccountStatsModal, {
      props: {
        show: true,
        account
      },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          LoadingSpinner: true,
          ModelDistributionChart: true,
          EndpointDistributionChart: true,
          Icon: true,
          Line: true
        }
      }
    })

    await wrapper.get('[data-testid="view-account-usage-history"]').trigger('click')

    expect(resolve).toHaveBeenCalledWith({
      name: 'AdminAccountUsageHistory',
      params: { id: 42 }
    })
    expect(open).toHaveBeenCalledWith(
      '/admin/accounts/42/usage-history',
      '_blank',
      'noopener,noreferrer'
    )
    wrapper.unmount()
  })
})
