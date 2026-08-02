import { describe, expect, it } from 'vitest'
import { opsDisplayedErrorCount } from '../opsFormatters'

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
