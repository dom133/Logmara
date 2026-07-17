import { createContext, useContext, useState, useEffect, useCallback, useRef, type ReactNode } from 'react'
import { api, refreshAccessToken } from './api'

interface User {
  id: number
  username: string
  role: string
  is_admin: boolean
  is_active: boolean
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

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(() => localStorage.getItem('token'))
  const [user, setUser] = useState<User | null>(null)
  const [sessionTimeout, setSessionTimeoutValue] = useState<number | null>(300)
  const [isSessionExpiringSoon, setIsSessionExpiringSoon] = useState(false)
  const [showSessionWarning, setShowSessionWarning] = useState(false)
  const [sessionWarningCountdown, setSessionWarningCountdown] = useState(0)
  const warningTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const countdownRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const extendingRef = useRef(false)

  const clearAllTimers = useCallback(() => {
    if (warningTimerRef.current) {
      clearTimeout(warningTimerRef.current)
      warningTimerRef.current = null
    }
    if (countdownRef.current) {
      clearTimeout(countdownRef.current)
      countdownRef.current = null
    }
  }, [])

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

  const setupSessionWarning = useCallback((tok: string) => {
    clearAllTimers()
    try {
      const base64 = tok.split('.')[1].replace(/-/g, '+').replace(/_/g, '/').padEnd(tok.split('.')[1].length + (4 - (tok.split('.')[1].length % 4)) % 4, '=')
      const payload = JSON.parse(atob(base64))
      const currentTime = Math.floor(Date.now() / 1000)
      const expiresIn = payload.exp - currentTime

      if (expiresIn <= 0) {
        return
      }

      const delayBeforeWarning = (expiresIn - 30) * 1000

      if (delayBeforeWarning <= 0) {
        setIsSessionExpiringSoon(true)
        setShowSessionWarning(true)
        setSessionWarningCountdown(Math.max(1, expiresIn))
        return
      }

      warningTimerRef.current = setTimeout(() => {
        setIsSessionExpiringSoon(true)
        setShowSessionWarning(true)
        setSessionWarningCountdown(30)
      }, delayBeforeWarning)
    } catch (e) {
      console.error('Error parsing token:', e)
    }
  }, [clearAllTimers])

  const logout = useCallback(async () => {
    try {
      await api.post('/auth/logout')
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
    clearAllTimers()
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
  }, [clearAllTimers, setupSessionWarning, logout])

  useEffect(() => {
    if (token) {
      api.defaults.headers.common['Authorization'] = `Bearer ${token}`
      loadUser()
      setupSessionWarning(token)
    }
  }, [token, loadUser, setupSessionWarning])

  useEffect(() => {
    if (showSessionWarning && sessionWarningCountdown > 0) {
      countdownRef.current = setTimeout(() => {
        setSessionWarningCountdown(prev => prev - 1)
      }, 1000)
    } else if (showSessionWarning && sessionWarningCountdown === 0 && !extendingRef.current) {
      logout()
    }
    return () => {
      if (countdownRef.current) {
        clearTimeout(countdownRef.current)
      }
    }
  }, [showSessionWarning, sessionWarningCountdown, logout])

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