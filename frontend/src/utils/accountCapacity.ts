export type AccountCapacityError =
  | 'concurrencyNonNegativeInteger'
  | 'concurrencyMustBePositiveInteger'
  | 'reserveNonNegativeInteger'
  | 'reserveMustBeZeroWhenUnlimited'
  | 'reserveMustBeLessThanConcurrency'

export interface AccountCapacityValidation {
  concurrency: number | null
  reserve: number | null
  general: number | null
  unlimited: boolean
  concurrencyError: AccountCapacityError | null
  reserveError: AccountCapacityError | null
  valid: boolean
}

export const isNonNegativeInteger = (value: unknown): value is number =>
  typeof value === 'number' &&
  Number.isFinite(value) &&
  Number.isInteger(value) &&
  value >= 0

export function validateAccountCapacity(
  concurrencyValue: unknown,
  reserveValue: unknown,
  options: { allowUnlimited?: boolean } = {}
): AccountCapacityValidation {
  const concurrency = isNonNegativeInteger(concurrencyValue) ? concurrencyValue : null
  const reserve = isNonNegativeInteger(reserveValue) ? reserveValue : null
  const concurrencyError =
    concurrency === null
      ? 'concurrencyNonNegativeInteger'
      : concurrency === 0 && options.allowUnlimited === false
        ? 'concurrencyMustBePositiveInteger'
        : null

  let reserveError: AccountCapacityError | null =
    reserve === null ? 'reserveNonNegativeInteger' : null
  if (concurrency !== null && reserve !== null) {
    if (concurrency === 0 && reserve !== 0) {
      reserveError = 'reserveMustBeZeroWhenUnlimited'
    } else if (concurrency > 0 && reserve >= concurrency) {
      reserveError = 'reserveMustBeLessThanConcurrency'
    }
  }

  const valid = concurrencyError === null && reserveError === null
  return {
    concurrency,
    reserve,
    general: valid && concurrency !== null && reserve !== null
      ? concurrency === 0
        ? 0
        : concurrency - reserve
      : null,
    unlimited: valid && concurrency === 0,
    concurrencyError,
    reserveError,
    valid
  }
}
