<template>
  <AppLayout>
    <div class="space-y-6" data-testid="account-usage-history-page">
      <section class="card p-5">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div class="flex min-w-0 items-start gap-3">
            <button
              type="button"
              class="mt-0.5 rounded-lg p-2 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-800 dark:text-gray-400 dark:hover:bg-dark-700 dark:hover:text-gray-100"
              :title="t('admin.accounts.history.backToAccounts')"
              @click="goBack"
            >
              <Icon name="arrowLeft" size="md" />
            </button>
            <div class="min-w-0">
              <h1 class="truncate text-xl font-semibold text-gray-900 dark:text-white">
                {{ account?.name || t('admin.accounts.history.title') }}
              </h1>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t('admin.accounts.history.accountId', { id: accountId || '-' }) }}
              </p>
              <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">
                {{ t('admin.accounts.history.subtitle') }}
              </p>
              <p v-if="accountLoading" class="mt-2 text-sm text-gray-500 dark:text-gray-400">
                {{ t('common.loading') }}
              </p>
              <p v-if="accountError" class="mt-2 text-sm text-red-600 dark:text-red-400">
                {{ accountError }}
              </p>
            </div>
          </div>
          <span
            v-if="account"
            :class="[
              'rounded-full px-3 py-1 text-xs font-semibold',
              account.status === 'active'
                ? 'bg-green-100 text-green-700 dark:bg-green-500/20 dark:text-green-400'
                : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
            ]"
          >
            {{ account.status }}
          </span>
        </div>
      </section>

      <section class="card p-4">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="inline-flex rounded-lg bg-gray-100 p-1 dark:bg-dark-700">
            <button
              type="button"
              data-testid="granularity-day"
              :class="granularityButtonClass('day')"
              @click="setGranularity('day')"
            >
              {{ t('admin.accounts.history.byDay') }}
            </button>
            <button
              type="button"
              data-testid="granularity-week"
              :class="granularityButtonClass('week')"
              @click="setGranularity('week')"
            >
              {{ t('admin.accounts.history.byWeek') }}
            </button>
          </div>
          <p v-if="history" class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.history.timezone', { timezone: history.timezone }) }}
          </p>
        </div>
      </section>

      <div v-if="history" class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <div class="card p-4">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.history.totalQuotaUsed') }}
          </p>
          <p class="mt-2 text-2xl font-bold text-emerald-600 dark:text-emerald-400">
            ${{ formatCost(history.summary.total_account_cost) }}
          </p>
        </div>
        <div class="card p-4">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.history.totalRequests') }}
          </p>
          <p class="mt-2 text-2xl font-bold text-gray-900 dark:text-white">
            {{ formatNumber(history.summary.total_requests) }}
          </p>
        </div>
        <div class="card p-4">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.history.totalTokens') }}
          </p>
          <p class="mt-2 text-2xl font-bold text-gray-900 dark:text-white">
            {{ formatNumber(history.summary.total_tokens) }}
          </p>
        </div>
        <div class="card p-4">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
            {{
              granularity === 'day'
                ? t('admin.accounts.history.activeDays')
                : t('admin.accounts.history.activeWeeks')
            }}
          </p>
          <p class="mt-2 text-2xl font-bold text-gray-900 dark:text-white">
            {{ formatNumber(history.summary.total_periods) }}
          </p>
          <p
            v-if="history.summary.first_period_start"
            class="mt-1 text-xs text-gray-500 dark:text-gray-400"
          >
            {{
              t('admin.accounts.history.range', {
                start: history.summary.first_period_start,
                end: history.summary.last_period_end
              })
            }}
          </p>
        </div>
      </div>

      <section class="card overflow-hidden">
        <div v-if="historyLoading" class="flex flex-col items-center justify-center gap-3 py-16">
          <LoadingSpinner />
          <p class="text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.history.loading') }}
          </p>
        </div>

        <div v-else-if="historyError" class="flex flex-col items-center justify-center gap-3 px-4 py-16 text-center">
          <Icon name="exclamationCircle" size="xl" class="text-red-500" />
          <p class="text-sm text-red-600 dark:text-red-400">{{ historyError }}</p>
          <button type="button" class="btn btn-secondary" @click="loadHistory">
            {{ t('admin.accounts.history.retry') }}
          </button>
        </div>

        <div
          v-else-if="!history || history.total === 0"
          class="flex flex-col items-center justify-center gap-3 px-4 py-16 text-center text-gray-500 dark:text-gray-400"
        >
          <Icon name="calendar" size="xl" />
          <p class="text-sm">{{ t('admin.accounts.history.noData') }}</p>
        </div>

        <template v-else>
          <div class="overflow-x-auto">
            <table class="min-w-full divide-y divide-gray-200 dark:divide-dark-700">
              <thead class="bg-gray-50 dark:bg-dark-800/70">
                <tr>
                  <th class="table-header-cell">{{ t('admin.accounts.history.period') }}</th>
                  <th class="table-header-cell text-right">{{ t('admin.accounts.history.quotaUsed') }}</th>
                  <th class="table-header-cell text-right">{{ t('admin.accounts.history.requests') }}</th>
                  <th class="table-header-cell text-right">{{ t('admin.accounts.history.tokens') }}</th>
                  <th class="table-header-cell text-right">{{ t('admin.accounts.history.standardCost') }}</th>
                  <th class="table-header-cell text-right">{{ t('admin.accounts.history.userCost') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 bg-white dark:divide-dark-700 dark:bg-dark-800">
                <tr v-for="item in history.items" :key="item.period_start">
                  <td class="table-body-cell whitespace-nowrap font-medium text-gray-900 dark:text-white">
                    {{ formatPeriod(item.period_start, item.period_end) }}
                  </td>
                  <td class="table-body-cell whitespace-nowrap text-right font-semibold text-emerald-600 dark:text-emerald-400">
                    ${{ formatCost(item.account_cost) }}
                  </td>
                  <td class="table-body-cell whitespace-nowrap text-right">
                    {{ formatNumber(item.requests) }}
                  </td>
                  <td class="table-body-cell whitespace-nowrap text-right">
                    {{ formatNumber(item.tokens) }}
                  </td>
                  <td class="table-body-cell whitespace-nowrap text-right">
                    ${{ formatCost(item.standard_cost) }}
                  </td>
                  <td class="table-body-cell whitespace-nowrap text-right">
                    ${{ formatCost(item.user_cost) }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <Pagination
            v-if="history.total > 0"
            :page="history.page"
            :total="history.total"
            :page-size="history.page_size"
            :page-size-options="[20, 50, 100]"
            @update:page="handlePageChange"
            @update:pageSize="handlePageSizeChange"
          />
        </template>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Pagination from '@/components/common/Pagination.vue'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import type {
  Account,
  AccountUsageHistoryGranularity,
  AccountUsageHistoryResponse
} from '@/types'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const appStore = useAppStore()

const account = ref<Account | null>(null)
const history = ref<AccountUsageHistoryResponse | null>(null)
const accountLoading = ref(false)
const historyLoading = ref(false)
const accountError = ref('')
const historyError = ref('')
const granularity = ref<AccountUsageHistoryGranularity>('day')
const page = ref(1)
const pageSize = ref(50)
let historyRequestSequence = 0

const accountId = computed(() => {
  const raw = Array.isArray(route.params.id) ? route.params.id[0] : route.params.id
  if (typeof raw !== 'string' || !/^\d+$/.test(raw)) return 0
  const parsed = Number(raw)
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : 0
})

const loadAccount = async () => {
  if (!accountId.value) return
  accountLoading.value = true
  accountError.value = ''
  try {
    account.value = await adminAPI.accounts.getById(accountId.value)
  } catch (error) {
    accountError.value = extractApiErrorMessage(
      error,
      t('admin.accounts.history.accountLoadError')
    )
    appStore.showError(accountError.value)
  } finally {
    accountLoading.value = false
  }
}

const loadHistory = async () => {
  if (!accountId.value) return
  const requestSequence = ++historyRequestSequence
  historyLoading.value = true
  historyError.value = ''
  try {
    const response = await adminAPI.accounts.getUsageHistory(accountId.value, {
      granularity: granularity.value,
      page: page.value,
      page_size: pageSize.value
    })
    if (requestSequence !== historyRequestSequence) return
    history.value = response
    page.value = response.page
    pageSize.value = response.page_size
  } catch (error) {
    if (requestSequence !== historyRequestSequence) return
    historyError.value = extractApiErrorMessage(error, t('admin.accounts.history.loadError'))
    appStore.showError(historyError.value)
  } finally {
    if (requestSequence === historyRequestSequence) {
      historyLoading.value = false
    }
  }
}

const setGranularity = (value: AccountUsageHistoryGranularity) => {
  if (granularity.value === value) return
  granularity.value = value
  page.value = 1
  void loadHistory()
}

const handlePageChange = (value: number) => {
  page.value = value
  void loadHistory()
}

const handlePageSizeChange = (value: number) => {
  pageSize.value = value
  page.value = 1
  void loadHistory()
}

const goBack = () => {
  void router.push({ name: 'AdminAccounts' })
}

const granularityButtonClass = (value: AccountUsageHistoryGranularity) => [
  'rounded-md px-4 py-2 text-sm font-medium transition-colors',
  granularity.value === value
    ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-600 dark:text-primary-300'
    : 'text-gray-500 hover:text-gray-800 dark:text-gray-400 dark:hover:text-gray-100'
]

const formatPeriod = (start: string, end: string) => {
  return start === end ? start : `${start} – ${end}`
}

const formatNumber = (value: number) => new Intl.NumberFormat().format(value)

const formatCost = (value: number) =>
  new Intl.NumberFormat(undefined, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 6
  }).format(value)

onMounted(() => {
  if (!accountId.value) {
    const message = t('admin.accounts.history.invalidAccount')
    accountError.value = message
    historyError.value = message
    return
  }
  void Promise.all([loadAccount(), loadHistory()])
})
</script>

<style scoped>
.table-header-cell {
  @apply px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400;
}

.table-body-cell {
  @apply px-4 py-3 text-sm text-gray-700 dark:text-gray-300;
}
</style>
