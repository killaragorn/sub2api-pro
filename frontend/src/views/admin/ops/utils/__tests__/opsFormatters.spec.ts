import { describe, expect, it } from 'vitest'

import { formatMemorySizeMB, opsDisplayedErrorCount } from '../opsFormatters'

describe('opsDisplayedErrorCount', () => {
  it('keeps explicit SLA exclusions in operational error counts', () => {
    expect(opsDisplayedErrorCount(5, 0)).toBe(5)
  })

  it('removes the separately displayed business-limit bucket', () => {
    expect(opsDisplayedErrorCount(8, 3)).toBe(5)
  })

  it('never returns a negative or non-finite count', () => {
    expect(opsDisplayedErrorCount(2, 3)).toBe(0)
    expect(opsDisplayedErrorCount(Number.NaN, 1)).toBe(0)
  })
})

describe('formatMemorySizeMB', () => {
  it('keeps capacities below one GiB in MB', () => {
    expect(formatMemorySizeMB(61)).toBe('61 MB')
    expect(formatMemorySizeMB(1023)).toBe('1023 MB')
  })

  it('converts capacities at or above one GiB to GB', () => {
    expect(formatMemorySizeMB(1024)).toBe('1 GB')
    expect(formatMemorySizeMB(1536)).toBe('1.5 GB')
    expect(formatMemorySizeMB(32768)).toBe('32 GB')
  })

  it('handles zero and invalid values without producing a misleading unit', () => {
    expect(formatMemorySizeMB(0)).toBe('0 MB')
    expect(formatMemorySizeMB(-1)).toBe('-')
    expect(formatMemorySizeMB(Number.NaN)).toBe('-')
    expect(formatMemorySizeMB(Number.POSITIVE_INFINITY)).toBe('-')
    expect(formatMemorySizeMB(null)).toBe('-')
    expect(formatMemorySizeMB(undefined)).toBe('-')
  })
})
