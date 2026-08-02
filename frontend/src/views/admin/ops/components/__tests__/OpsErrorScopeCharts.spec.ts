import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import OpsErrorDistributionChart from '../OpsErrorDistributionChart.vue'
import OpsErrorTrendChart from '../OpsErrorTrendChart.vue'

vi.mock('chart.js', () => ({
  Chart: { register: vi.fn() },
  ArcElement: {},
  CategoryScale: {},
  Filler: {},
  Legend: {},
  LineElement: {},
  LinearScale: {},
  PointElement: {},
  Title: {},
  Tooltip: {},
}))

vi.mock('vue-chartjs', async () => {
  const { defineComponent } = await import('vue')

  return {
    Doughnut: defineComponent({
      name: 'Doughnut',
      props: {
        data: { type: Object, required: true },
        options: { type: Object, default: () => ({}) },
      },
      template: '<div class="doughnut-stub" />',
    }),
    Line: defineComponent({
      name: 'LineChartStub',
      props: {
        data: { type: Object, required: true },
        options: { type: Object, default: () => ({}) },
      },
      template: '<div class="line-stub" />',
    }),
  }
})

vi.mock('../../utils/opsFormatters', () => ({
  formatHistoryLabel: (date: string | undefined) => date ?? '',
  opsDisplayedErrorCount: (total: number | null | undefined, business: number | null | undefined) =>
    Math.max((total ?? 0) - (business ?? 0), 0),
  sumNumbers: (values: Array<number | null | undefined>) =>
    values.reduce<number>((total, value) => total + (typeof value === 'number' && Number.isFinite(value) ? value : 0), 0),
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()

  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

const HelpTooltipStub = defineComponent({
  name: 'HelpTooltip',
  props: {
    content: { type: String, default: '' },
  },
  template: '<span class="help-tooltip-stub" />',
})

const EmptyStateStub = defineComponent({
  name: 'EmptyState',
  props: {
    title: { type: String, default: '' },
    description: { type: String, default: '' },
  },
  template: '<div class="empty-state-stub" />',
})

const globalStubs = {
  stubs: {
    HelpTooltip: HelpTooltipStub,
    EmptyState: EmptyStateStub,
  },
}

describe('Ops operational error charts', () => {
  it('错误分布图使用运营错误数，不把业务限制错误算进请求错误分布', () => {
    const wrapper = mount(OpsErrorDistributionChart, {
      props: {
        loading: false,
        data: {
          total: 10,
          items: [
            { status_code: 400, total: 7, sla: 2, business_limited: 5 },
            { status_code: 503, total: 3, sla: 0, business_limited: 3 },
          ],
        },
      },
      global: globalStubs,
    })

    const doughnut = wrapper.findComponent({ name: 'Doughnut' })
    expect(doughnut.exists()).toBe(true)
    expect(doughnut.props('data')).toMatchObject({
      labels: ['admin.ops.client'],
      datasets: [{ data: [2] }],
    })
  })

  it('错误分布图在只有业务限制错误时显示为空态', () => {
    const wrapper = mount(OpsErrorDistributionChart, {
      props: {
        loading: false,
        data: {
          total: 4,
          items: [{ status_code: 500, total: 4, sla: 0, business_limited: 4 }],
        },
      },
      global: globalStubs,
    })

    expect(wrapper.findComponent({ name: 'Doughnut' }).exists()).toBe(false)
    expect(wrapper.find('.empty-state-stub').exists()).toBe(true)
  })

  it('错误分布图保留显式 SLA 排除项', () => {
    const wrapper = mount(OpsErrorDistributionChart, {
      props: {
        loading: false,
        data: {
          total: 3,
          items: [{ status_code: 200, total: 3, sla: 0, business_limited: 0 }],
        },
      },
      global: globalStubs,
    })

    const doughnut = wrapper.findComponent({ name: 'Doughnut' })
    expect(doughnut.exists()).toBe(true)
    expect(doughnut.props('data')).toMatchObject({
      labels: ['admin.ops.other'],
      datasets: [{ data: [3] }],
    })
  })

  it('错误趋势图在只有业务限制错误时禁用请求错误详情', () => {
    const wrapper = mount(OpsErrorTrendChart, {
      props: {
        loading: false,
        timeRange: '1h',
        points: [
          {
            bucket_start: '2026-05-18T00:00:00Z',
            error_count_total: 5,
            business_limited_count: 5,
            error_count_sla: 0,
            upstream_error_count_excl_429_529: 0,
            upstream_429_count: 0,
            upstream_529_count: 0,
          },
        ],
      },
      global: globalStubs,
    })

    const requestErrorsButton = wrapper.findAll('button')[0]
    expect(requestErrorsButton.attributes('disabled')).toBeDefined()
  })

  it('错误趋势图使用运营错误数启用详情，即使这些错误不计入 SLA', () => {
    const wrapper = mount(OpsErrorTrendChart, {
      props: {
        loading: false,
        timeRange: '1h',
        points: [
          {
            bucket_start: '2026-08-02T00:00:00Z',
            error_count_total: 5,
            business_limited_count: 0,
            error_count_sla: 0,
            upstream_error_count_excl_429_529: 0,
            upstream_429_count: 0,
            upstream_529_count: 0,
          },
        ],
      },
      global: globalStubs,
    })

    const requestErrorsButton = wrapper.findAll('button')[0]
    expect(requestErrorsButton.attributes('disabled')).toBeUndefined()
    const line = wrapper.findComponent({ name: 'LineChartStub' })
    expect(line.props('data').datasets[0]).toMatchObject({
      label: 'admin.ops.requestErrors',
      data: [5],
    })
  })
})
