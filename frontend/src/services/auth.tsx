import { createContext, useContext, useState, useEffect, useCallback, useRef, type ReactNode } from 'react'
import { api } from './api'

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

  const clearAllTimers = useCallback(() => {
    if (checkIntervalRef.current) {
      clearInterval(checkIntervalRef.current)
      checkIntervalRef.current = null
    }
    sessionExpiryRef.current = null
  }, [])

  const logout = useCallback(async () => {
    try {
      await api.post('/auth/logout', {})
    } catch (e) {
      console.error('Error during logout:', e)
    }
    setUser(null)
    setLoading(false)
    clearAllTimers()
    setIsSessionExpiringSoon(false)
    setShowSessionWarning(false)
    setSessionWarningCountdown(0)
  }, [clearAllTimers])

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
      const expiresAt = res.data.expires_at
      if (expiresAt) {
        setupSessionWarning(expiresAt)
      }
      setLoading(false)
    } catch {
      setUser(null)
      setLoading(false)
      clearAllTimers()
      setIsSessionExpiringSoon(false)
      setShowSessionWarning(false)
      setSessionWarningCountdown(0)
    }
  }, [clearAllTimers, setupSessionWarning])

  const login = useCallback(async (username: string, password: string) => {
    try {
      const res = await api.post('/auth/login', { username, password })
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
