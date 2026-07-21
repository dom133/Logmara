import { createContext, useContext, useState, useEffect, useCallback, useRef, type ReactNode } from 'react'
import { api, refreshAccessToken } from './api'

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
  token: string | null
  user: User | null
  login: (username: string, password: string) => Promise<{ ok: boolean; error?: string }>
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
}

const AuthContext = createContext<AuthContextType>({} as AuthContextType)

const WARNING_LEAD_MS = 30000
const EXPIRY_CHECK_INTERVAL_MS = 1000

// Decodes the JWT's `exp` claim without verifying the signature - this is
// only used to schedule the client-side warning/kill timers, never for
// authorization decisions (the server independently rejects expired tokens).
function decodeExpiryMs(tok: string): number | null {
  try {
    const part = tok.split('.')[1]
    const base64 = part.replace(/-/g, '+').replace(/_/g, '/')
    const padded = base64.padEnd(base64.length + (4 - (base64.length % 4)) % 4, '=')
    const payload = JSON.parse(atob(padded))
    return typeof payload.exp === 'number' ? payload.exp * 1000 : null
  } catch (e) {
    console.error('Error parsing token:', e)
    return null
  }
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(() => localStorage.getItem('token'))
  const [user, setUser] = useState<User | null>(null)
  const [sessionTimeout, setSessionTimeoutValue] = useState<number | null>(300)
  const [isSessionExpiringSoon, setIsSessionExpiringSoon] = useState(false)
  const [showSessionWarning, setShowSessionWarning] = useState(false)
  const [sessionWarningCountdown, setSessionWarningCountdown] = useState(0)
  const sessionExpiryRef = useRef<number | null>(null)
  const checkIntervalRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const extendingRef = useRef(false)

  const clearAllTimers = useCallback(() => {
    if (checkIntervalRef.current) {
      clearInterval(checkIntervalRef.current)
      checkIntervalRef.current = null
    }
    sessionExpiryRef.current = null
  }, [])

  const logout = useCallback(async () => {
    try {
      const refreshToken = localStorage.getItem('refresh_token')
      await api.post('/auth/logout', { refresh_token: refreshToken })
    } catch (e) {
      console.error('Error during logout:', e)
    }
    setToken(null)
    setUser(null)
    localStorage.removeItem('token')
    localStorage.removeItem('refresh_token')
    delete api.defaults.headers.common['Authorization']
    clearAllTimers()
    setIsSessionExpiringSoon(false)
    setShowSessionWarning(false)
    setSessionWarningCountdown(0)
  }, [clearAllTimers])

  const loadUser = useCallback(async () => {
    try {
      const res = await api.get('/auth/me')
      setUser(res.data)
    } catch {
      setToken(null)
      setUser(null)
      localStorage.removeItem('token')
      localStorage.removeItem('refresh_token')
      delete api.defaults.headers.common['Authorization']
      clearAllTimers()
      setIsSessionExpiringSoon(false)
      setShowSessionWarning(false)
      setSessionWarningCountdown(0)
    }
  }, [clearAllTimers])

  // Authoritative expiry check, driven off the real clock rather than a
  // single scheduled setTimeout. Called from a 1s interval and again
  // immediately whenever the tab regains visibility, so a throttled/paused
  // background tab can't let the token silently outlive its real expiry -
  // once time is actually up and the user hasn't extended, the session is
  // killed. There is no silent background refresh here.
  const checkSessionExpiry = useCallback(() => {
    const expiresAt = sessionExpiryRef.current
    if (expiresAt === null || extendingRef.current) return

    const msLeft = expiresAt - Date.now()

    if (msLeft <= 0) {
      logout()
      return
    }

    if (msLeft <= WARNING_LEAD_MS) {
      setIsSessionExpiringSoon(true)
      setShowSessionWarning(true)
      setSessionWarningCountdown(Math.max(1, Math.ceil(msLeft / 1000)))
    }
  }, [logout])

  const setupSessionWarning = useCallback((tok: string) => {
    clearAllTimers()
    const expiresAt = decodeExpiryMs(tok)
    if (expiresAt === null) return

    sessionExpiryRef.current = expiresAt
    checkSessionExpiry()
    checkIntervalRef.current = setInterval(checkSessionExpiry, EXPIRY_CHECK_INTERVAL_MS)
  }, [clearAllTimers, checkSessionExpiry])

  const login = useCallback(async (username: string, password: string) => {
    try {
      const res = await api.post('/auth/login', { username, password })
      const t = res.data.token
      const rt = res.data.refresh_token
      setToken(t)
      localStorage.setItem('token', t)
      localStorage.setItem('refresh_token', rt)
      api.defaults.headers.common['Authorization'] = `Bearer ${t}`
      loadUser()
      return { ok: true }
    } catch (error: any) {
      return { ok: false, error: error.response?.data?.message || 'Login failed' }
    }
  }, [loadUser])

  const extendSession = useCallback(async () => {
    extendingRef.current = true
    try {
      const refreshToken = localStorage.getItem('refresh_token')
      if (!refreshToken) return

      const data = await refreshAccessToken(refreshToken)
      const newToken = data.token
      const newRT = data.refresh_token
      setToken(newToken)
      localStorage.setItem('token', newToken)
      localStorage.setItem('refresh_token', newRT)
      api.defaults.headers.common['Authorization'] = `Bearer ${newToken}`

      setIsSessionExpiringSoon(false)
      setShowSessionWarning(false)
      setSessionWarningCountdown(0)

      setupSessionWarning(newToken)
    } catch (e) {
      console.error('Error extending session:', e)
      logout()
    } finally {
      extendingRef.current = false
    }
  }, [setupSessionWarning, logout])

  useEffect(() => {
    if (token) {
      api.defaults.headers.common['Authorization'] = `Bearer ${token}`
      loadUser()
      setupSessionWarning(token)
    }
  }, [token, loadUser, setupSessionWarning])

  // Catches up immediately when a backgrounded/throttled tab regains focus,
  // instead of waiting for the next interval tick - a tab that was asleep
  // past the token's real expiry gets killed the moment it wakes up.
  useEffect(() => {
    const onVisibilityChange = () => {
      if (document.visibilityState === 'visible') {
        checkSessionExpiry()
      }
    }
    document.addEventListener('visibilitychange', onVisibilityChange)
    return () => document.removeEventListener('visibilitychange', onVisibilityChange)
  }, [checkSessionExpiry])

  // Mirrors auth state across tabs via the native `storage` event (fires in
  // every other tab sharing this origin's localStorage). If one tab
  // refreshes the token, other tabs adopt it instead of racing their own
  // refresh; if one tab's session dies, the rest die with it immediately
  // instead of showing a stale "logged in" UI.
  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      if (e.key !== 'token') return
      if (e.newValue === null) {
        setToken(null)
        setUser(null)
        delete api.defaults.headers.common['Authorization']
        clearAllTimers()
        setIsSessionExpiringSoon(false)
        setShowSessionWarning(false)
        setSessionWarningCountdown(0)
      } else if (e.newValue !== token) {
        setToken(e.newValue)
      }
    }
    window.addEventListener('storage', onStorage)
    return () => window.removeEventListener('storage', onStorage)
  }, [token, clearAllTimers])

  const isAdmin = user?.is_admin || false
  const canEdit = user?.role === 'admin' || user?.role === 'editor'

  return (
    <AuthContext.Provider value={{
      token,
      user,
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
      extendSession
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
