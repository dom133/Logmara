import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { AuthProvider, useAuth } from './auth'
import { api, checkSession } from './api'

vi.mock('./api', () => ({
  api: { get: vi.fn(), post: vi.fn() },
  checkSession: vi.fn(),
}))

const mockedApi = api as unknown as { get: ReturnType<typeof vi.fn>; post: ReturnType<typeof vi.fn> }
const mockedCheckSession = checkSession as unknown as ReturnType<typeof vi.fn>

describe('AuthProvider loadUser', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockedCheckSession.mockResolvedValue(undefined)
  })

  it('sets the user straight away when the access token is still valid', async () => {
    mockedApi.get.mockResolvedValueOnce({ data: { id: 1, username: 'alice', expires_at: Date.now() / 1000 + 900 } })

    const { result } = renderHook(() => useAuth(), { wrapper: AuthProvider })

    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.user).toMatchObject({ username: 'alice' })
    expect(mockedApi.post).not.toHaveBeenCalled()
  })

  it('falls back to a silent /auth/refresh and restores the session when the access token expired but a "remember this device" refresh token is still valid', async () => {
    mockedApi.get
      .mockRejectedValueOnce({ response: { status: 401 } }) // GET /auth/me - expired access token
      .mockResolvedValueOnce({ data: { id: 1, username: 'alice', expires_at: Date.now() / 1000 + 900 } }) // GET /auth/me (retry)
    mockedApi.post.mockResolvedValueOnce({ data: { success: true, expires_at: Date.now() / 1000 + 900 } }) // POST /auth/refresh

    const { result } = renderHook(() => useAuth(), { wrapper: AuthProvider })

    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(mockedApi.post).toHaveBeenCalledWith('/auth/refresh', {}, { headers: { 'X-Silent-Refresh': 'true' } })
    expect(mockedApi.get).toHaveBeenCalledTimes(2)
    expect(result.current.user).toMatchObject({ username: 'alice' })
  })

  it('logs out cleanly when there is no refresh token at all', async () => {
    mockedApi.get.mockRejectedValueOnce({ response: { status: 401 } }) // GET /auth/me
    mockedApi.post.mockRejectedValueOnce({ response: { status: 401 } }) // POST /auth/refresh - no refresh token cookie

    const { result } = renderHook(() => useAuth(), { wrapper: AuthProvider })

    await waitFor(() => expect(result.current.loading).toBe(false), { timeout: 5000 })
    expect(result.current.user).toBeNull()
  })

  it('logs out cleanly on reload when the session was not set up with "remember this device", even though its refresh token is still valid', async () => {
    // Mirrors the backend: handler.Refresh rejects a silent (X-Silent-Refresh)
    // request when the refresh token's `remember` column is false, without
    // consuming the token - modeled here simply as a 401, since loadUser
    // reacts to it the same way either way.
    mockedApi.get.mockRejectedValueOnce({ response: { status: 401 } }) // GET /auth/me - expired access token
    mockedApi.post.mockRejectedValueOnce({ response: { status: 401 }, message: 'silent refresh not allowed for this session' })

    const { result } = renderHook(() => useAuth(), { wrapper: AuthProvider })

    await waitFor(() => expect(result.current.loading).toBe(false), { timeout: 5000 })
    expect(mockedApi.post).toHaveBeenCalledWith('/auth/refresh', {}, { headers: { 'X-Silent-Refresh': 'true' } })
    expect(result.current.user).toBeNull()
  })

  it('silently renews an expiring "remember this device" session instead of showing the warning modal', async () => {
    // 25s < WARNING_LEAD_MS (30s), so setupSessionWarning's own initial
    // checkSessionExpiry() call fires the auto-renew immediately - no need
    // to wait for the 1s interval tick.
    mockedApi.get.mockResolvedValueOnce({
      data: { id: 1, username: 'alice', expires_at: Date.now() / 1000 + 25, remembered: true },
    })
    mockedApi.post.mockResolvedValueOnce({ data: { success: true, expires_at: Date.now() / 1000 + 900, remembered: true } })

    const { result } = renderHook(() => useAuth(), { wrapper: AuthProvider })
    await waitFor(() => expect(result.current.loading).toBe(false))

    await waitFor(() => expect(mockedApi.post).toHaveBeenCalledWith('/auth/refresh', {}))
    expect(result.current.showSessionWarning).toBe(false)
  })

  it('shows the warning modal for an expiring session that was not "remembered"', async () => {
    mockedApi.get.mockResolvedValueOnce({
      data: { id: 1, username: 'alice', expires_at: Date.now() / 1000 + 25, remembered: false },
    })

    const { result } = renderHook(() => useAuth(), { wrapper: AuthProvider })
    await waitFor(() => expect(result.current.loading).toBe(false))

    await waitFor(() => expect(result.current.showSessionWarning).toBe(true))
    expect(mockedApi.post).not.toHaveBeenCalled()
  })
})
