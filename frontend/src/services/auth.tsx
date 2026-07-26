import { createContext, useContext, useState, useEffect, useCallback, useRef, type ReactNode } from 'react'
import { api, checkSession } from './api'

interface User {
  id: number
  username: string
  role: string
  is_admin: boolean
  is_active: boolean
  notifications_enabled: boolean
  relay_ingestion_enabled: boolean
}

interface AuthContextType {
  user: User | null
  loading: boolean
  login: (username: string, password: string, remember?: boolean) => Promise<{ ok: boolean; error?: string }>
  logout: () => Promise<void>
  isAdmin: boolean
  canEdit: boolean
  sessionTimeout: number | null
  setSessionTimeout: (timeout: number) => void
  isSessionExpiringSoon: boolean
  showSessionWarning: boolean
  setShowSessionWarning: (show: boolean) => void
  sessionWarningCountdown: number
  extendSession: () => Promise<void>
  refreshUser: () => Promise<void>
}

const AuthContext = createContext<AuthContextType>({} as AuthContextType)

const WARNING_LEAD_MS = 30000
const EXPIRY_CHECK_INTERVAL_MS = 1000

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null)
  const [loading, setLoading] = useState(true)
  const [sessionTimeout, setSessionTimeoutValue] = useState<number | null>(300)
  const [isSessionExpiringSoon, setIsSessionExpiringSoon] = useState(false)
  const [showSessionWarning, setShowSessionWarning] = useState(false)
  const [sessionWarningCountdown, setSessionWarningCountdown] = useState(0)
  const sessionExpiryRef = useRef<number | null>(null)
  const checkIntervalRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const extendingRef = useRef(false)
  const rememberedRef = useRef(false)
  // Bridges checkSessionExpiry -> extendSession without a circular useCallback
  // dependency (extendSession -> setupSessionWarning -> checkSessionExpiry).
  const extendSessionRef = useRef<() => Promise<void>>(async () => {})

  const clearAllTimers = useCallback(() => {
    if (checkIntervalRef.current) {
      clearInterval(checkIntervalRef.current)
      checkIntervalRef.current = null
    }
    sessionExpiryRef.current = null
  }, [])

  const resetToLoggedOut = useCallback(() => {
    setUser(null)
    setLoading(false)
    clearAllTimers()
    rememberedRef.current = false
    setIsSessionExpiringSoon(false)
    setShowSessionWarning(false)
    setSessionWarningCountdown(0)
  }, [clearAllTimers])

  const logout = useCallback(async () => {
    try {
      await api.post('/auth/logout', {})
    } catch (e) {
      console.error('Error during logout:', e)
    }
    resetToLoggedOut()
  }, [resetToLoggedOut])

  const checkSessionExpiry = useCallback(() => {
    const expiresAt = sessionExpiryRef.current
    if (expiresAt === null || extendingRef.current) return

    const msLeft = expiresAt - Date.now()

    if (msLeft <= 0) {
      logout()
      return
    }

    if (msLeft <= WARNING_LEAD_MS) {
      // Devices with "remember this device" checked at login are trusted to
      // renew on their own - interrupting them with the same countdown modal
      // as a non-remembered session defeats the point of remembering them.
      if (rememberedRef.current) {
        extendSessionRef.current()
        return
      }
      setIsSessionExpiringSoon(true)
      setShowSessionWarning(true)
      setSessionWarningCountdown(Math.max(1, Math.ceil(msLeft / 1000)))
    }
  }, [logout])

  const setupSessionWarning = useCallback((expiresAtUnix: number) => {
    clearAllTimers()
    sessionExpiryRef.current = expiresAtUnix * 1000
    checkSessionExpiry()
    checkIntervalRef.current = setInterval(checkSessionExpiry, EXPIRY_CHECK_INTERVAL_MS)
  }, [clearAllTimers, checkSessionExpiry])

  const loadUser = useCallback(async () => {
    try {
      const res = await api.get('/auth/me')
      setUser(res.data)
      rememberedRef.current = !!res.data.remembered
      const expiresAt = res.data.expires_at
      if (expiresAt) {
        setupSessionWarning(expiresAt)
      }
      setLoading(false)
    } catch {
      // Access token missing/expired on boot (browser reopened, tab reloaded
      // after its short TTL). Try the refresh token once before giving up -
      // this is what makes "remember this device" persist. The silent header
      // tells the backend to reject this when the session wasn't set up with
      // "remember" (see handler.Refresh) - those sessions are only meant to
      // last as long as the access token / an actively open, extended tab.
      try {
        const refreshRes = await api.post('/auth/refresh', {}, { headers: { 'X-Silent-Refresh': 'true' } })
        const meRes = await api.get('/auth/me')
        setUser(meRes.data)
        rememberedRef.current = !!refreshRes.data.remembered
        if (refreshRes.data.expires_at) {
          setupSessionWarning(refreshRes.data.expires_at)
        }
        setLoading(false)
      } catch {
        resetToLoggedOut()
      }
    }
  }, [resetToLoggedOut, setupSessionWarning])

  const login = useCallback(async (username: string, password: string, remember?: boolean) => {
    try {
      const res = await api.post('/auth/login', { username, password, remember: !!remember })
      const userData = res.data.user
      setUser({
        id: userData.id,
        username: userData.username,
        role: userData.role,
        is_admin: userData.is_admin,
        is_active: userData.is_active,
        notifications_enabled: userData.notifications_enabled ?? true,
        relay_ingestion_enabled: userData.relay_ingestion_enabled ?? false,
      })
      rememberedRef.current = !!res.data.remembered
      setupSessionWarning(res.data.expires_at)
      return { ok: true }
    } catch (error: any) {
      return { ok: false, error: error.response?.data?.error || 'Login failed' }
    }
  }, [setupSessionWarning])

  const extendSession = useCallback(async () => {
    extendingRef.current = true
    try {
      const res = await api.post('/auth/refresh', {})
      rememberedRef.current = !!res.data.remembered
      setupSessionWarning(res.data.expires_at)
      setIsSessionExpiringSoon(false)
      setShowSessionWarning(false)
      setSessionWarningCountdown(0)
    } catch (e) {
      console.error('Error extending session:', e)
      logout()
    } finally {
      extendingRef.current = false
    }
  }, [setupSessionWarning, logout])

  useEffect(() => {
    extendSessionRef.current = extendSession
  }, [extendSession])

  useEffect(() => {
    loadUser()
  }, [loadUser])

  useEffect(() => {
    const onVisibilityChange = () => {
      if (document.visibilityState === 'visible') {
        checkSessionExpiry()
      }
    }
    document.addEventListener('visibilitychange', onVisibilityChange)
    return () => document.removeEventListener('visibilitychange', onVisibilityChange)
  }, [checkSessionExpiry])

  // Poll GET /auth/session-check every 30s while logged in, purely to
  // notice a server-side revocation (Admin, another device's "Sign out" in
  // My Sessions, or this session's own Logout) quickly instead of waiting
  // for the access token's own natural expiry. A 401 here is already
  // handled by the axios response interceptor (redirects to /login), so
  // this just needs to fire the request - errors are swallowed rather than
  // handled twice.
  useEffect(() => {
    if (!user) return
    const interval = setInterval(() => {
      checkSession().catch(() => { /* handled by the response interceptor */ })
    }, 30000)
    return () => clearInterval(interval)
  }, [user])

  const isAdmin = user?.is_admin || false
  const canEdit = user?.role === 'admin' || user?.role === 'editor'

  return (
    <AuthContext.Provider value={{
      user,
      loading,
      login,
      logout,
      isAdmin,
      canEdit,
      sessionTimeout,
      setSessionTimeout: setSessionTimeoutValue,
      isSessionExpiringSoon,
      showSessionWarning,
      setShowSessionWarning,
      sessionWarningCountdown,
      extendSession,
      refreshUser: loadUser
    }}>
      {children}
    </AuthContext.Provider>
  )
}

export const useAuth = () => {
  const context = useContext(AuthContext)
  if (!context) {
    throw new Error('useAuth must be used within an AuthProvider')
  }
  return context
}
