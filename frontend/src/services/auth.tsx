import { createContext, useContext, useState, useEffect, useCallback, useRef, type ReactNode } from 'react'
import { useLocation } from 'react-router-dom'
import { message } from 'antd'
import { t } from 'i18next'
import { api, checkSession, reportActivity } from './api'
import { getErrorMessage } from '../utils/error'

interface User {
  id: number
  username: string
  role: string
  is_admin: boolean
  is_active: boolean
  notifications_enabled: boolean
  relay_ingestion_enabled: boolean
  cloud_bridge_enabled: boolean
  password_expires_at?: number
  password_expired?: boolean
}

interface AuthContextType {
  user: User | null
  loading: boolean
  refreshing: boolean
  login: (username: string, password: string, remember?: boolean) => Promise<{ ok: boolean; error?: string; passwordExpired?: boolean }>
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
  showPasswordExpired: boolean
  setShowPasswordExpired: (show: boolean) => void
}

const AuthContext = createContext<AuthContextType>({} as AuthContextType)

const WARNING_LEAD_MS = 30000
const SILENT_EXTEND_LEAD_MS = 60000
const EARLY_WARNING_LEAD_MS = 60000
const EXPIRY_CHECK_INTERVAL_FAST_MS = 1000
const EXPIRY_CHECK_INTERVAL_NORMAL_MS = 5000
const EXPIRY_CHECK_INTERVAL_SLOW_MS = 30000
const EXTEND_RETRY_MAX = 3
const EXTEND_RETRY_BASE_MS = 2000
const SILENT_REFRESH_RETRY_MAX = 3
const SILENT_REFRESH_RETRY_BASE_MS = 2000
const SESSION_CHECK_INTERVAL_ACTIVE_MS = 30000
const SESSION_CHECK_INTERVAL_IDLE_MS = 300000
const SESSION_CHECK_BACKOFF_FACTOR = 2
const SESSION_CHECK_BACKOFF_MAX_MS = 300000
const STORAGE_KEY_EXPIRY = '__session_expiry_ts__'
const STORAGE_KEY_LAST_ACTIVE = '__session_last_active__'

export function AuthProvider({ children }: { children: ReactNode }) {
  // A single AuthProvider now covers the whole app (including /login) so
  // that a successful login is visible to PrivateRoute immediately - it
  // used to be a second, separate provider instance just for /login, which
  // meant logging in there never updated the auth state PrivateRoute reads,
  // bouncing the user straight back to /login after a successful login.
  // Session polling / activity tracking are still skipped while on /login
  // (checked via the route instead of a prop) since there's nothing to
  // poll or track for a session that doesn't exist yet.
  const location = useLocation()
  const onLoginPage = location.pathname === '/login'
  const [user, setUser] = useState<User | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [sessionTimeout, setSessionTimeoutValue] = useState<number | null>(300)
  const [isSessionExpiringSoon, setIsSessionExpiringSoon] = useState(false)
  const [showSessionWarning, setShowSessionWarning] = useState(false)
  const [sessionWarningCountdown, setSessionWarningCountdown] = useState(0)
  const [showPasswordExpired, setShowPasswordExpired] = useState(false)
  const sessionExpiryRef = useRef<number | null>(null)
  const checkIntervalRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const extendingRef = useRef(false)
  const rememberedRef = useRef(false)
  const earlyWarningShownRef = useRef(false)
  // Tracks real user interaction (click, keydown, scroll) without throttling.
  // Used to decide whether to auto-extend the session or show the expiry modal.
  const userActiveRef = useRef(0)
  const ACTIVITY_IDLE_THRESHOLD_MS = 3 * 60 * 1000 // 3 min
  // Bridges checkSessionExpiry -> extendSession without a circular useCallback
  // dependency (extendSession -> setupSessionWarning -> checkSessionExpiry).
  const extendSessionRef = useRef<((retryCount?: number) => Promise<void>)>(async () => {})
  // Stable ref for setupSessionWarning so trySilentRefresh doesn't recreate
  // on every checkSessionExpiry change, preventing loadUser/login from
  // re-firing on minor internal updates.
  const setupSessionWarningRef = useRef<((expiresAtUnix: number, noImmediateCheck?: boolean) => void)>(() => {})
  // Stable ref for resetSessionCheckBackoff so reportUserActivity can call
  // it without depending on it (which would cause re-renders on every event).
  const resetSessionCheckBackoffRef = useRef<(() => void)>(() => {})

  const clearAllTimers = useCallback(() => {
    if (checkIntervalRef.current) {
      clearInterval(checkIntervalRef.current)
      checkIntervalRef.current = null
    }
    sessionExpiryRef.current = null
    earlyWarningShownRef.current = false
  }, [])

  const resetToLoggedOut = useCallback(() => {
    setUser(null)
    setLoading(false)
    clearAllTimers()
    rememberedRef.current = false
    userActiveRef.current = 0
    setIsSessionExpiringSoon(false)
    setShowSessionWarning(false)
    setSessionWarningCountdown(0)
    localStorage.removeItem(STORAGE_KEY_EXPIRY)
    localStorage.removeItem(STORAGE_KEY_LAST_ACTIVE)
  }, [clearAllTimers])

  const logout = useCallback(async () => {
    try {
      await api.post('/auth/logout', {})
    } catch (e) {
      console.error('Error during logout:', e)
    }
    resetToLoggedOut()
  }, [resetToLoggedOut])

  const getAdaptiveInterval = useCallback((msLeft: number): number => {
    if (msLeft <= SILENT_EXTEND_LEAD_MS * 2) return EXPIRY_CHECK_INTERVAL_FAST_MS
    if (msLeft <= 300000) return EXPIRY_CHECK_INTERVAL_NORMAL_MS
    return EXPIRY_CHECK_INTERVAL_SLOW_MS
  }, [])

  const checkSessionExpiry = useCallback(() => {
    const expiresAt = sessionExpiryRef.current
    if (expiresAt === null || extendingRef.current) return

    const storedExpiry = localStorage.getItem(STORAGE_KEY_EXPIRY)
    if (storedExpiry) {
      const storedTs = parseInt(storedExpiry, 10)
      if (storedTs > expiresAt) {
        sessionExpiryRef.current = storedTs
      }
    }

    const msLeft = expiresAt - Date.now()

    if (msLeft <= 0) {
      if (rememberedRef.current) {
        extendSessionRef.current()
        return
      }
      logout()
      return
    }

    const extendThreshold = rememberedRef.current ? SILENT_EXTEND_LEAD_MS : WARNING_LEAD_MS

    if (msLeft <= extendThreshold) {
      if (rememberedRef.current) {
        extendSessionRef.current()
        return
      }
      // If the user has been interacting with the page recently, auto-extend
      // silently instead of showing the expiry modal. The modal is only for
      // truly idle sessions that have gone without user interaction.
      const wasActive = Date.now() - userActiveRef.current < ACTIVITY_IDLE_THRESHOLD_MS
      if (wasActive) {
        extendSessionRef.current()
        return
      }
      setIsSessionExpiringSoon(true)
      setShowSessionWarning(true)
      setSessionWarningCountdown(Math.max(1, Math.ceil(msLeft / 1000)))
      return
    }

    if (!rememberedRef.current && msLeft <= EARLY_WARNING_LEAD_MS && !earlyWarningShownRef.current) {
      earlyWarningShownRef.current = true
      message.info(t('sessionWarning.earlyWarning'), 5)
    }

    if (checkIntervalRef.current) {
      clearInterval(checkIntervalRef.current)
    }
    const adaptiveInterval = getAdaptiveInterval(msLeft)
    checkIntervalRef.current = setInterval(checkSessionExpiry, adaptiveInterval)
  }, [logout, getAdaptiveInterval])

  const setupSessionWarning = useCallback((expiresAtUnix: number, noImmediateCheck?: boolean) => {
    clearAllTimers()
    const expiryMs = expiresAtUnix * 1000
    sessionExpiryRef.current = expiryMs
    localStorage.setItem(STORAGE_KEY_EXPIRY, expiryMs.toString())
    localStorage.setItem(STORAGE_KEY_LAST_ACTIVE, Date.now().toString())
    if (!noImmediateCheck) {
      checkSessionExpiry()
    }
    checkIntervalRef.current = setInterval(checkSessionExpiry, EXPIRY_CHECK_INTERVAL_NORMAL_MS)
  }, [clearAllTimers, checkSessionExpiry])

  const trySilentRefresh = useCallback(async (retryCount = 0): Promise<boolean> => {
    try {
      const refreshRes = await api.post('/auth/refresh', {}, { headers: { 'X-Silent-Refresh': 'true' } })
      const meRes = await api.get('/auth/me')
      setUser(meRes.data)
      rememberedRef.current = !!refreshRes.data.remembered
      if (refreshRes.data.expires_at) {
        // Skip immediate checkSessionExpiry: the token was just rotated,
        // so it has a full TTL. Calling checkSessionExpiry right away
        // would trigger extendSession if the access token TTL is under
        // SILENT_EXTEND_LEAD_MS, causing a second rotation and a
        // duplicate session row in the database.
        setupSessionWarningRef.current(refreshRes.data.expires_at, true)
      }
      return true
    } catch (e) {
      console.error('Silent refresh failed:', e)
      // A response means the server definitively said no (400: no refresh
      // token at all - e.g. a first-time anonymous visitor; 401: token
      // exists but isn't a "remembered" session). Neither improves on
      // retry, so retrying just stalls the redirect to /login for ~14s
      // (2s+4s+8s) on every logged-out page load. Only retry when there's
      // no response (network hiccup/timeout) or the server itself errored
      // (5xx) - those are the actually transient cases.
      const status = (e as { response?: { status?: number } })?.response?.status
      const isRetryable = status === undefined || status >= 500
      if (isRetryable && retryCount < SILENT_REFRESH_RETRY_MAX) {
        const delay = SILENT_REFRESH_RETRY_BASE_MS * Math.pow(2, retryCount)
        await new Promise(r => setTimeout(r, delay))
        return trySilentRefresh(retryCount + 1)
      }
      return false
    }
  }, [])

  const loadUser = useCallback(async () => {
    try {
      const res = await api.get('/auth/me')
      if (res.data.password_expired) {
        setShowPasswordExpired(true)
      }
      setUser(res.data)
      rememberedRef.current = !!res.data.remembered
      const expiresAt = res.data.expires_at
      if (expiresAt) {
        setupSessionWarningRef.current(expiresAt)
      }
      setLoading(false)
    } catch {
      // Access token missing/expired on boot (browser reopened, tab reloaded
      // after its short TTL). Try the refresh token before giving up -
      // this is what makes "remember this device" persist. The silent header
      // tells the backend to reject this when the session wasn't set up with
      // "remember" (see handler.Refresh) - those sessions are only meant to
      // last as long as the access token / an actively open, extended tab.
      setRefreshing(true)
      const ok = await trySilentRefresh()
      if (!ok) {
        resetToLoggedOut()
      } else {
        setLoading(false)
      }
      setRefreshing(false)
    }
  }, [resetToLoggedOut, trySilentRefresh])

  const login = useCallback(async (username: string, password: string, remember?: boolean) => {
    try {
      const res = await api.post('/auth/login', { username, password, remember: !!remember })
      if (res.data.password_expired) {
        setShowPasswordExpired(true)
        return { ok: false, passwordExpired: true }
      }
      const userData = res.data.user
      setUser({
        id: userData.id,
        username: userData.username,
        role: userData.role,
        is_admin: userData.is_admin,
        is_active: userData.is_active,
        notifications_enabled: userData.notifications_enabled ?? true,
        relay_ingestion_enabled: userData.relay_ingestion_enabled ?? false,
        cloud_bridge_enabled: userData.cloud_bridge_enabled ?? false,
        password_expires_at: res.data.user?.password_expires_at,
      })
      rememberedRef.current = !!res.data.remembered
      setupSessionWarningRef.current(res.data.expires_at)
      return { ok: true }
    } catch (error) {
      return { ok: false, error: getErrorMessage(error, t('login.loginFailed')) }
    }
  }, [])

  const extendSession = useCallback(async (retryCount = 0) => {
    extendingRef.current = true
    try {
      const res = await api.post('/auth/refresh', {})
      rememberedRef.current = !!res.data.remembered
      setupSessionWarningRef.current(res.data.expires_at)
      setIsSessionExpiringSoon(false)
      setShowSessionWarning(false)
      setSessionWarningCountdown(0)
    } catch (e) {
      console.error('Error extending session:', e)
      if (rememberedRef.current && retryCount < EXTEND_RETRY_MAX) {
        const delay = EXTEND_RETRY_BASE_MS * Math.pow(2, retryCount)
        setTimeout(() => extendSession(retryCount + 1), delay)
        return
      }
      extendingRef.current = false
      logout()
    }
  }, [logout])

  useEffect(() => {
    extendSessionRef.current = extendSession
  }, [extendSession])

  useEffect(() => {
    setupSessionWarningRef.current = setupSessionWarning
  }, [setupSessionWarning])

  useEffect(() => {
    loadUser()
  }, [loadUser])

  const sessionCheckTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const sessionCheckBackoffRef = useRef(SESSION_CHECK_INTERVAL_ACTIVE_MS)

  const scheduleSessionCheck = useCallback((backoffMs?: number) => {
    if (sessionCheckTimerRef.current) {
      clearTimeout(sessionCheckTimerRef.current)
    }
    const interval = backoffMs ?? sessionCheckBackoffRef.current
    sessionCheckTimerRef.current = setTimeout(async () => {
      await checkSession().catch(() => { /* handled by the response interceptor */ })
      sessionCheckBackoffRef.current = Math.min(
        sessionCheckBackoffRef.current * SESSION_CHECK_BACKOFF_FACTOR,
        SESSION_CHECK_BACKOFF_MAX_MS
      )
      scheduleSessionCheck()
    }, interval)
  }, [])

  const resetSessionCheckBackoff = useCallback(() => {
    sessionCheckBackoffRef.current = SESSION_CHECK_INTERVAL_ACTIVE_MS
  }, [])

  useEffect(() => {
    resetSessionCheckBackoffRef.current = resetSessionCheckBackoff
  }, [resetSessionCheckBackoff])

  useEffect(() => {
    if (onLoginPage) return
    const onVisibilityChange = () => {
      if (document.visibilityState === 'visible') {
        checkSessionExpiry()
        resetSessionCheckBackoff()
      }
    }
    document.addEventListener('visibilitychange', onVisibilityChange)
    return () => document.removeEventListener('visibilitychange', onVisibilityChange)
  }, [checkSessionExpiry, resetSessionCheckBackoff, onLoginPage])

  // Poll GET /auth/session-check while logged in, purely to notice a
  // server-side revocation (Admin, another device's "Sign out" in My
  // Sessions, or this session's own Logout) quickly instead of waiting
  // for the access token's own natural expiry. Uses exponential backoff
  // when the tab is idle to reduce unnecessary requests - starts at 30s,
  // doubles on each successful check, caps at 5min. Backoff resets on
  // visibility change (tab brought to foreground). A 401 here is already
  // handled by the axios response interceptor (redirects to /login).
  useEffect(() => {
    if (!user || onLoginPage) return
    scheduleSessionCheck()
    return () => {
      if (sessionCheckTimerRef.current) {
        clearTimeout(sessionCheckTimerRef.current)
        sessionCheckTimerRef.current = null
      }
    }
  }, [user, onLoginPage, scheduleSessionCheck])

  // Track user activity (click, keydown, scroll) and report to backend
  // so the inactivity timer is reset. Throttled to 1 call per 10 seconds.
  const lastActivityRef = useRef(0)
  const ACTIVITY_THROTTLE_MS = 10000

  const reportUserActivity = useCallback(() => {
    if (!user) return
    const now = Date.now()
    // Always update the activity tracker regardless of throttle
    userActiveRef.current = now
    // Throttled backend report and backoff reset
    if (now - lastActivityRef.current < ACTIVITY_THROTTLE_MS) return
    lastActivityRef.current = now
    localStorage.setItem(STORAGE_KEY_LAST_ACTIVE, now.toString())
    resetSessionCheckBackoffRef.current()
    reportActivity().catch(() => { /* non-critical */ })
  }, [user])

  useEffect(() => {
    if (!user || onLoginPage) return
    const opts = { capture: true, passive: true }
    const targets = ['mousedown', 'keydown', 'scroll'] as const
    targets.forEach(evt => window.addEventListener(evt, reportUserActivity, opts))
    return () => {
      targets.forEach(evt => window.removeEventListener(evt, reportUserActivity, opts))
    }
  }, [user, reportUserActivity, onLoginPage])

  const isAdmin = user?.is_admin || false
  const canEdit = user?.role === 'admin' || user?.role === 'editor'

  return (
    <AuthContext.Provider value={{
      user,
      loading,
      refreshing,
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
      refreshUser: loadUser,
      showPasswordExpired,
      setShowPasswordExpired,
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
