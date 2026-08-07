import { useEffect, useState, createContext, useContext } from 'react'
import { BrowserRouter, Routes, Route, Navigate, useLocation, Link as RouterLink } from 'react-router-dom'
import { Layout, theme, Spin, Result, ConfigProvider, Button, Drawer, Space, Typography, Skeleton } from 'antd'
import { DashboardOutlined, FileTextOutlined, SettingOutlined, FundOutlined, SafetyOutlined, BellOutlined, PushpinOutlined, SunOutlined, MoonOutlined, MenuOutlined, LogoutOutlined, UserOutlined, NodeIndexOutlined } from '@ant-design/icons'
import { initI18n, initI18nFallback } from './i18n'
import { useTranslation } from 'react-i18next'
import Login from './pages/Login'
import Dashboard from './pages/Dashboard'
import LogsViewer from './pages/LogsViewer'
import ParsersPage from './pages/Parsers'
import DashboardsPage from './pages/Dashboards'
import DashboardViewPage from './pages/DashboardView'
import AlertsPage from './pages/Alerts'

import Admin from './pages/Admin'
import SyslogRelay from './pages/SyslogRelay'
import SetupWizard from './pages/SetupWizard'
import ErrorBoundary from './components/ErrorBoundary'
import { SessionWarningModal } from './components/SessionWarningModal'
import { PasswordExpiryWarning } from './components/PasswordExpiryWarning'
import { SessionsModal } from './components/SessionsModal'
import { NotificationBell } from './components/NotificationBell'
import { AuthProvider, useAuth } from './services/auth'
import { getDashboards, getDashboard, Dashboard as DashboardType, checkInitialized, getVersion } from './services/api'
import { useIsMobile } from './hooks/useIsMobile'
import { I18nextProvider } from 'react-i18next'
import type { i18n as I18nInstance } from 'i18next'

const { Sider, Content, Header, Footer } = Layout

function NavContent({ location, user, logout, isAdmin, pinnedDashboards, loadingDashboards, collapsed, onClose }: {
  location: ReturnType<typeof useLocation>
  user: { username?: string; notifications_enabled?: boolean; relay_ingestion_enabled?: boolean } | undefined
  logout: () => void
  isAdmin: boolean
  pinnedDashboards: DashboardType[]
  loadingDashboards: boolean
  collapsed?: boolean
  onClose?: () => void
}) {
  const { token } = theme.useToken()
  const { themeMode } = useTheme()
  const { t } = useTranslation()
  const activeBg = themeMode === 'dark' ? '#2a2a2a' : '#e6f7ff'
  const renderLinks = () => (
    <>
      <nav>
        {navItems.filter(item =>
          !(item.adminOnly && !isAdmin) &&
          !(item.hideWhenNotificationsDisabled && user?.notifications_enabled === false) &&
          !(item.hideWhenRelayDisabled && user?.relay_ingestion_enabled !== true)
        ).map(item => (
          <RouterLink
            key={item.key}
            to={item.key}
            onClick={onClose}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 8,
              padding: '12px 16px',
              textDecoration: 'none',
              color: location.pathname === item.key ? '#1890ff' : token.colorText,
              background: location.pathname === item.key ? activeBg : 'transparent',
              borderRadius: 4,
              margin: '2px 8px',
              fontSize: 14,
            }}
          >
            <span style={{ fontSize: 18 }}>{item.icon}</span>
            {!collapsed && t(item.labelKey)}
          </RouterLink>
        ))}
        {loadingDashboards ? (
          <div style={{ padding: '8px 16px' }}>
            <Skeleton.Input size="small" style={{ marginBottom: 8 }} />
            <Skeleton.Input size="small" style={{ marginBottom: 8 }} />
          </div>
        ) : pinnedDashboards.length > 0 && (
          <>
            {!collapsed && (
            <div style={{ padding: '12px 16px 4px', fontSize: 11, color: token.colorTextTertiary, textTransform: 'uppercase', letterSpacing: 1 }}>
              {t('nav.pinned')}
            </div>
          )}
            {pinnedDashboards.map(d => (
              <RouterLink
                key={`pin-${d.id}`}
                to={`/dashboards/${d.id}`}
                onClick={onClose}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 8,
                  padding: '12px 16px',
                  textDecoration: 'none',
                  color: location.pathname === `/dashboards/${d.id}` ? '#1890ff' : token.colorText,
                  background: location.pathname === `/dashboards/${d.id}` ? activeBg : 'transparent',
                  borderRadius: 4,
                  margin: '2px 8px',
                  fontSize: 14,
                }}
              >
                <span style={{ fontSize: 18 }}><PushpinOutlined /></span>
                {!collapsed && d.name}
              </RouterLink>
            ))}
          </>
        )}
      </nav>
    </>
  )

  return (
    <>
      <div style={{ padding: '20px 16px', fontSize: 20, fontWeight: 700, color: '#1890ff', display: 'flex', alignItems: 'center', gap: 8 }}>
        <img src="/icons/icon-192.png" alt="Logmara" style={{ width: 28, height: 28, borderRadius: 6 }} /> {!collapsed && 'Logmara'}
      </div>
      {renderLinks()}
    </>
  )
}

type ThemeMode = 'light' | 'dark'

const ThemeContext = createContext<{ themeMode: ThemeMode; toggleTheme: () => void }>({ themeMode: 'light', toggleTheme: () => {} })

export function useTheme() {
  return useContext(ThemeContext)
}

function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [themeMode, setThemeMode] = useState<ThemeMode>(() => {
    const saved = localStorage.getItem('syslog_theme')
    return (saved === 'dark' || saved === 'light') ? saved : 'light'
  })

  const toggleTheme = () => {
    setThemeMode(prev => {
      const next = prev === 'light' ? 'dark' : 'light'
      localStorage.setItem('syslog_theme', next)
      return next
    })
  }

  return (
    <ThemeContext.Provider value={{ themeMode, toggleTheme }}>
      <ConfigProvider theme={{
        algorithm: themeMode === 'dark' ? theme.darkAlgorithm : theme.defaultAlgorithm,
        token: { colorError: '#ff4d4f' },
      }}>
        <style>{`
          html, body { margin: 0; padding: 0; height: 100%; width: 100%; }
          body { background: ${themeMode === 'dark' ? '#141414' : '#f0f2f5'}; }
          .ant-message-error .anticon { color: #ff4d4f !important; }
          .ant-message-error .ant-message-notice-content { border-color: #ff4d4f !important; background: #fff2f0 !important; }
          .ant-message-error { color: #ff4d4f !important; }
          @media (max-width: 768px) { .navbar-text-label { display: none !important; } }
        `}</style>
        {children}
      </ConfigProvider>
    </ThemeContext.Provider>
  )
}

const navItems = [
  { key: '/', labelKey: 'nav.dashboard', icon: <DashboardOutlined /> },
  { key: '/logs', labelKey: 'nav.logs', icon: <FileTextOutlined /> },
  { key: '/parsers', labelKey: 'nav.parsers', icon: <SettingOutlined /> },
  { key: '/dashboards', labelKey: 'nav.dashboards', icon: <FundOutlined /> },
  { key: '/alerts', labelKey: 'nav.alerts', icon: <BellOutlined />, hideWhenNotificationsDisabled: true },
  { key: '/relay', labelKey: 'nav.relay', icon: <NodeIndexOutlined />, adminOnly: true, hideWhenRelayDisabled: true },
  { key: '/admin', labelKey: 'nav.admin', icon: <SafetyOutlined />, adminOnly: true },
]

function AppLayout({ children }: { children: React.ReactNode }) {
  const { user, logout, isAdmin, showSessionWarning, sessionWarningCountdown, extendSession } = useAuth()
  const { token } = theme.useToken()
  const { themeMode, toggleTheme } = useTheme()
  const { t } = useTranslation()
  const location = useLocation()
  const [pinnedDashboards, setPinnedDashboards] = useState<DashboardType[]>([])
  const [loadingDashboards, setLoadingDashboards] = useState(true)
  const [collapsed, setCollapsed] = useState(false)
  const [drawerVisible, setDrawerVisible] = useState(false)
  const [sessionsModalOpen, setSessionsModalOpen] = useState(false)
  const [appVersion, setAppVersion] = useState('')
  const isMobile = useIsMobile()

  useEffect(() => {
    if (!user) return
    const load = async () => {
      setLoadingDashboards(true)
      try {
        const dashboards = await getDashboards()
        setPinnedDashboards(dashboards.filter((d: DashboardType) => d.pinned))
      } catch { /* ignore */ } finally {
        setLoadingDashboards(false)
      }
    }
    load()
    const handler = () => load()
    window.addEventListener('dashboards-pinned-changed', handler)
    return () => window.removeEventListener('dashboards-pinned-changed', handler)
  }, [user])

  useEffect(() => {
    if (!user) return
    getVersion().then(r => setAppVersion(r.version)).catch(() => {})
  }, [user])

  useEffect(() => {
    if (isMobile) {
      setCollapsed(true)
    }
  }, [isMobile])

  const [dashboardTitle, setDashboardTitle] = useState<string | null>(null)

  useEffect(() => {
    const match = location.pathname.match(/^\/dashboards\/([^/]+)$/)?.[1]
    if (match) {
      setDashboardTitle(null)
      const load = async () => {
        try {
          const d = await getDashboard(parseInt(match, 10))
          setDashboardTitle(d.name)
        } catch { /* ignore */ }
      }
      load()
    } else {
      setDashboardTitle(null)
    }
  }, [location.pathname])

  return (
    <Layout style={{ minHeight: '100vh', background: token.colorBgContainer }}>
      <Layout>
        {!isMobile && (
          <Sider
            width={220}
            collapsedWidth={80}
            collapsed={collapsed}
            onCollapse={setCollapsed}
            style={{ background: token.colorBgContainer }}
            theme={themeMode === 'dark' ? 'dark' : 'light'}
          >
            <NavContent location={location} user={user ?? undefined} logout={logout} isAdmin={isAdmin} pinnedDashboards={pinnedDashboards} loadingDashboards={loadingDashboards} collapsed={collapsed} />
          </Sider>
        )}
        {isMobile && (
          <Drawer
            title=""
            placement="left"
            onClose={() => setDrawerVisible(false)}
            open={drawerVisible}
            width="85%"
            styles={{ body: { padding: 0 } }}
          >
            <div style={{ background: token.colorBgContainer, height: '100%' }}>
              <NavContent location={location} user={user ?? undefined} logout={logout} isAdmin={isAdmin} pinnedDashboards={pinnedDashboards} loadingDashboards={loadingDashboards} onClose={() => setDrawerVisible(false)} />
            </div>
          </Drawer>
        )}
        <Layout>
          <Header style={{ background: token.colorBgContainer, padding: '0 16px', display: 'flex', alignItems: 'center', justifyContent: 'space-between', borderBottom: 0 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
              {isMobile && (
                <Button
                  type="text"
                  icon={<MenuOutlined />}
                  onClick={() => setDrawerVisible(true)}
                />
              )}
              {!isMobile && (
                <Button
                  type="text"
                  icon={<MenuOutlined />}
                  onClick={() => setCollapsed(!collapsed)}
                />
              )}
              <span style={{ fontSize: 16, fontWeight: 500 }}>
                {(() => {
                  if (location.pathname === '/') return t('nav.dashboard')
                  const match = location.pathname.match(/^\/dashboards\/([^/]+)$/)?.[1]
                  if (match && dashboardTitle) return `${t('nav.dashboards')} / ${dashboardTitle}`
                  const key = `nav.${location.pathname.replace('/', '')}`
                  return t(key) || (location.pathname.replace('/', '').charAt(0).toUpperCase() + location.pathname.slice(2) || 'Logmara')
                })()}
              </span>
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'nowrap', overflow: 'hidden' }}>
              <NotificationBell />
              <Button
                type="text"
                onClick={() => setSessionsModalOpen(true)}
                style={{ fontSize: 13, color: token.colorTextSecondary, display: 'flex', alignItems: 'center', gap: 4, whiteSpace: 'nowrap' }}
                title={t('nav.sessions')}
              >
                <UserOutlined /> {user?.username}
              </Button>
              <Button
                type="text"
                icon={themeMode === 'dark' ? <SunOutlined /> : <MoonOutlined />}
                onClick={toggleTheme}
              >
                <span className="navbar-text-label">{themeMode === 'dark' ? t('nav.themeLight') : t('nav.themeDark')}</span>
              </Button>
              <Button
                type="text"
                danger
                icon={<LogoutOutlined />}
                onClick={logout}
              >
                <span className="navbar-text-label">{t('nav.logout')}</span>
              </Button>
            </div>
          </Header>
          <Content style={{ margin: 16, padding: 24, background: token.colorBgContainer, borderRadius: 8 }}>
            <PasswordExpiryWarning />
            <ErrorBoundary>{children}</ErrorBoundary>
          </Content>
          <Footer
            style={{
              background: token.colorPrimaryBg,
              color: token.colorPrimaryText,
              fontSize: 12,
              padding: '12px 16px',
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
              flexWrap: 'wrap',
              gap: 8,
            }}
          >
            <span>Logmara {appVersion}</span>
            <span>
              © {new Date().getFullYear()}{' '}
              <a href="https://github.com/dom133" target="_blank" rel="noopener noreferrer" style={{ color: 'inherit', textDecoration: 'underline' }}>
                Dominik Kruszewski
              </a>
              {' · '}
              <a href="https://github.com/dom133/Logmara" target="_blank" rel="noopener noreferrer" style={{ color: 'inherit', textDecoration: 'underline' }}>
                GitHub
              </a>
              {' · '}
              AGPL-3.0
            </span>
          </Footer>
        </Layout>
      </Layout>
      {showSessionWarning && (
        <SessionWarningModal
          countdown={sessionWarningCountdown}
          onExtend={extendSession}
          onLogout={logout}
        />
      )}
      <SessionsModal open={sessionsModalOpen} onClose={() => setSessionsModalOpen(false)} />
    </Layout>
  )
}

function PrivateRoute({ children, requireAdmin }: { children: React.ReactNode; requireAdmin?: boolean }) {
  const { user, isAdmin, loading } = useAuth()
  const location = useLocation()
  if (loading) return null
  if (!user) return <Navigate to={`/login?redirect=${encodeURIComponent(location.pathname)}`} replace />
  if (requireAdmin && !isAdmin) return <Navigate to="/" replace />
  return <AppLayout>{children}</AppLayout>
}

function NotFoundPage() {
  const { t } = useTranslation()
  return <Result status="404" title={t('notFound.title')} subTitle={t('notFound.subtitle')} />
}

export default function App() {
  const [initialized, setInitialized] = useState<boolean | null>(null)
  const [starting, setStarting] = useState(false)
  // Initialize a minimal i18n instance synchronously so the login page can
  // render immediately. The full init (real locale data + backend settings)
  // replaces these resources in the background.
  const fallbackI18n = initI18nFallback()
  const [i18nInstance] = useState<I18nInstance>(fallbackI18n)

  // Full i18n init (real locale data + backend settings) runs in the
  // background and replaces the fallback resources on the same global
  // i18n instance. Backend readiness check is independent.
  useEffect(() => {
    let cancelled = false

    const initI18nPromise = (async () => {
      try {
        await initI18n()
        if (cancelled) return
      } catch (e) {
        console.error('Failed to initialize i18n', e)
      }
    })()

    const check = async () => {
      try {
        const res = await checkInitialized()
        if (cancelled) return
        if (res.starting) {
          setStarting(true)
          setTimeout(() => check(), 2000)
        } else {
          setStarting(false)
          setInitialized(res.initialized)
        }
      } catch {
        // API unreachable (e.g. 502 while the backend container is still
        // coming up) - treat the same as "starting" and keep polling
        // instead of leaving the app stuck on a bare spinner forever.
        if (cancelled) return
        setStarting(true)
        setTimeout(() => check(), 2000)
      }
    }
    check()

    return () => { cancelled = true }
  }, [])

  if (starting || initialized === null) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', flexDirection: 'column', gap: 16, height: '100vh' }}>
        <Spin size="large" />
        <Typography.Text type="secondary">System starting... Please wait</Typography.Text>
      </div>
    )
  }

  return (
    <I18nextProvider i18n={i18nInstance}>
      <ThemeProvider>
        <BrowserRouter>
          <Routes>
          {!initialized ? (
            <>
              <Route path="/setup" element={<SetupWizard />} />
              <Route path="*" element={<Navigate to="/setup" replace />} />
            </>
          ) : (
            <>
              <AuthProvider skipInitialLoad>
                <Route path="/login" element={<Login />} />
              </AuthProvider>

              <AuthProvider>
                <Route path="/" element={<PrivateRoute><Dashboard /></PrivateRoute>} />
                <Route path="/logs" element={<PrivateRoute><LogsViewer /></PrivateRoute>} />
                <Route path="/parsers" element={<PrivateRoute><ParsersPage /></PrivateRoute>} />
                <Route path="/dashboards" element={<PrivateRoute><DashboardsPage /></PrivateRoute>} />
                <Route path="/dashboards/:id" element={<PrivateRoute><DashboardViewPage /></PrivateRoute>} />
                <Route path="/alerts" element={<PrivateRoute><AlertsPage /></PrivateRoute>} />
                <Route path="/admin" element={<PrivateRoute requireAdmin><Admin /></PrivateRoute>} />
                <Route path="/relay" element={<PrivateRoute requireAdmin><SyslogRelay /></PrivateRoute>} />
                <Route path="*" element={<PrivateRoute><NotFoundPage /></PrivateRoute>} />
              </AuthProvider>
            </>
          )}
          </Routes>
        </BrowserRouter>
      </ThemeProvider>
    </I18nextProvider>
  )
}