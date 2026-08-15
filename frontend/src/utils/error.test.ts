import { describe, it, expect } from 'vitest'
import { getErrorMessage } from './error'

describe('getErrorMessage', () => {
  it('returns fallback for null', () => {
    expect(getErrorMessage(null, 'fallback')).toBe('fallback')
  })

  it('returns fallback for undefined', () => {
    expect(getErrorMessage(undefined, 'fallback')).toBe('fallback')
  })

  it('returns fallback for empty object', () => {
    expect(getErrorMessage({}, 'fallback')).toBe('fallback')
  })

  it('returns Error message', () => {
    const err = new Error('something went wrong')
    expect(getErrorMessage(err, 'fallback')).toBe('something went wrong')
  })

  it('returns string directly', () => {
    expect(getErrorMessage('plain error', 'fallback')).toBe('plain error')
  })

  it('extracts error from axios-like response', () => {
    const axiosErr = {
      response: {
        data: { error: 'server error' },
      },
    }
    expect(getErrorMessage(axiosErr, 'fallback')).toBe('server error')
  })

  it('falls back when response.data.error is missing', () => {
    const axiosErr = {
      response: {
        data: {},
      },
    }
    expect(getErrorMessage(axiosErr, 'fallback')).toBe('fallback')
  })

  it('falls back when response.data is missing', () => {
    const axiosErr = {
      response: {},
    }
    expect(getErrorMessage(axiosErr, 'fallback')).toBe('fallback')
  })

  it('prefers response.data.error over Error message', () => {
    const err = new Error('error msg') as Error & { response?: { data?: { error?: string } } }
    err.response = { data: { error: 'api error' } }
    expect(getErrorMessage(err, 'fallback')).toBe('api error')
  })
})