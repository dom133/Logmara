import { createContext, useContext, useState, useEffect, type ReactNode } from 'react'
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
  extendSession: () => Promise<void>
}

const AuthContext = createContext<AuthContextType>({} as AuthContextType)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(() => localStorage.getItem('token'))
  const [user, setUser] = useState<User | null>(null)
  const [sessionTimeout, setSessionTimeoutValue] = useState<number | null>(300) // Default: 5 minutes
  const [isSessionExpiringSoon, setIsSessionExpiringSoon] = useState(false)
  const [showSessionWarning, setShowSessionWarning] = useState(false)
  const [tokenExpiryTimer, setTokenExpiryTimer] = useState<NodeJS.Timeout | null>(null)

  useEffect(() => {
    if (token) {
      api.defaults.headers.common['Authorization'] = `Bearer ${token}`
      loadUser()
      
      // Parse token to get expiry time
      try {
        const payload = JSON.parse(atob(token.split('.')[1]))
        const currentTime = Math.floor(Date.now() / 1000)
        const timeRemaining = payload.exp - currentTime
        
        if (timeRemaining > 0) {
          // Set up timer for session warnings
          const warningTime = Math.max(0, timeRemaining - 30) * 1000 // 30 seconds before expiration
          if (warningTime > 0 && warningTime < 300000) { // Don't show warning for very short sessions
            const timer = setTimeout(() => {
              setIsSessionExpiringSoon(true)
              setShowSessionWarning(true)
            }, warningTime)
            setTokenExpiryTimer(timer)
          }
        }
      } catch (e) {
        console.error('Error parsing token:', e)
      }
    }
  }, [token])

  const loadUser = async () => {
    try {
      const res = await api.get('/auth/me')
      setUser(res.data)
    } catch {
      logout()
    }
  }

  const login = async (username: string, password: string) => {
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
  }

  const logout = async () => {
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
    
    // Clear any pending timers
    if (tokenExpiryTimer) {
      clearTimeout(tokenExpiryTimer)
      setTokenExpiryTimer(null)
    }
    setIsSessionExpiringSoon(false)
    setShowSessionWarning(false)
  }

  const extendSession = async () => {
    try {
      const refreshToken = localStorage.getItem('refresh_token')
      if (!refreshToken) return
      
      const res = await refreshAccessToken(refreshToken)
      const newToken = res.data.token
      setToken(newToken)
      localStorage.setItem('token', newToken)
      api.defaults.headers.common['Authorization'] = `Bearer ${newToken}`
      
      // Reset the warning timer
      setIsSessionExpiringSoon(false)
      setShowSessionWarning(false)
      
      if (tokenExpiryTimer) {
        clearTimeout(tokenExpiryTimer)
        setTokenExpiryTimer(null)
      }
      
      // Set up new timer
      try {
        const payload = JSON.parse(atob(newToken.split('.')[1]))
        const currentTime = Math.floor(Date.now() / 1000)
        const timeRemaining = payload.exp - currentTime
        
        if (timeRemaining > 0) {
          const warningTime = Math.max(0, timeRemaining - 30) * 1000
          if (warningTime > 0 && warningTime < 300000) {
            const timer = setTimeout(() => {
              setIsSessionExpiringSoon(true)
              setShowSessionWarning(true)
            }, warningTime)
            setTokenExpiryTimer(timer)
          }
        }
      } catch (e) {
        console.error('Error parsing token:', e)
      }
    } catch (e) {
      console.error('Error extending session:', e)
      logout()
    }
  }

  const isAdmin = user?.is_admin || false
  const canEdit = user?.role === 'admin' || user?.role === 'editor'

  useEffect(() => {
    // Clean up timer on unmount
    return () => {
      if (tokenExpiryTimer) {
        clearTimeout(tokenExpiryTimer)
      }
    }
  }, [tokenExpiryTimer])

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