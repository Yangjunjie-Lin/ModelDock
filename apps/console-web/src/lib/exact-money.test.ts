import { describe, expect, it } from 'vitest'
import { addDecimalStrings, decimalRatioPercent, formatMoney } from './api'

describe('exact monetary display helpers', () => {
  it('adds decimal strings without binary floating-point rounding', () => {
    expect(addDecimalStrings(['0.1', '0.2'])).toBe('0.3')
    expect(addDecimalStrings(['999999999999.999999999999', '-0.000000000001'])).toBe('999999999999.999999999998')
  })

  it('preserves all significant decimal digits when formatting money', () => {
    expect(formatMoney('123456789012.100000000001', 'usd')).toBe('USD 123,456,789,012.100000000001')
  })

  it('uses exact integers until the final display-only percentage', () => {
    expect(decimalRatioPercent('0.1', '0.3')).toBe(33.3)
    expect(decimalRatioPercent('1', '0')).toBeNull()
  })
})
