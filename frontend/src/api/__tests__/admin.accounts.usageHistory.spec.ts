import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({
  get: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: { get }
}))

import { getUsageHistory } from '@/api/admin/accounts'

describe('admin accounts usage history API', () => {
  beforeEach(() => {
    get.mockReset()
    get.mockResolvedValue({ data: { items: [] } })
  })

  it('requests paginated daily or weekly account history', async () => {
    await getUsageHistory(42, {
      granularity: 'week',
      page: 3,
      page_size: 50
    })

    expect(get).toHaveBeenCalledWith('/admin/accounts/42/usage-history', {
      params: {
        granularity: 'week',
        page: 3,
        page_size: 50
      }
    })
  })
})
