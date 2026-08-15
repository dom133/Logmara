import { useEffect, useState, createContext, useContext, lazy, Suspense } from 'react'
import { BrowserRouter, Routes, Route, Navigate, useLocation, Link as RouterLink } from 'react-router-dom'
import { Layout, theme, Spin, Result, ConfigProvider, Button, Drawer, Typography, Skeleton } from 'antd'
import { DashboardOutlined, FileTextOutlined, SettingOutlined, FundOutlined, SafetyOutlined, BellOutlined, PushpinOutlined, SunOutlined, MoonOutlined, MenuOutlined, LogoutOutlined, UserOutlined, NodeIndexOutlined } from '@ant-design/icons'
import { initI18n, initI18nFallback } from './i18n'
import { useTranslation } from 'react-i18next'
import { tokens } from './theme/tokens'
import Login from './pages/Login'
import ErrorBoundary from './components/ErrorBoundary'
import { SessionWarningModal } from './components/SessionWarningModal'
import { PasswordExpiryWarning } from './components/PasswordExpiryWarning'
import { SessionsModal } from './components/SessionsModal'
import { NotificationBell } from './components/NotificationBell'
import BottomNav from './components/BottomNav'
import LiveIndicator from './components/LiveIndicator'
import PullToRefresh from './components/PullToRefresh'
import { AuthProvider, useAuth } from './services/auth'
import { getDashboards, getDashboard, Dashboard as DashboardType, checkInitialized, getVersion } from './services/api'
import { useIsMobile } from './hooks/useIsMobile'
import { I18nextProvider } from 'react-i18next'
import type { i18n as I18nInstance } from 'i18next'

// Lazy-loaded so a fresh page load only pays for the route actually being
// visited instead of downloading every page (incl. echarts-heavy Dashboard
// and the rarely-used Admin/SyslogRelay pages) up front in one bundle.
const Dashboard = lazy(() => import('./pages/Dashboard'))
const LogsViewer = lazy(() => import('./pages/LogsViewer'))
const ParsersPage = lazy(() => import('./pages/Parsers'))
const DashboardsPage = lazy(() => import('./pages/Dashboards'))
const DashboardViewPage = lazy(() => import('./pages/DashboardView'))
const AlertsPage = lazy(() => import('./pages/Alerts'))
const Admin = lazy(() => import('./pages/Admin'))
const SyslogRelay = lazy(() => import('./pages/SyslogRelay'))
const SetupWizard = lazy(() => import('./pages/SetupWizard'))

function RouteFallback() {
  return (
    <div style={{ padding: '60px 24px' }}>
      <Skeleton active paragraph={{ rows: 8 }} avatar />
    </div>
  )
}

const { Sider, Content, Header, Footer } = Layout

function NavLink({ to, isActive, onClick, icon, label, collapsed }: {
  to: string
  isActive: boolean
  onClick?: () => void
  icon: React.ReactNode
  label: string
  collapsed?: boolean
}) {
  const { token } = theme.useToken()
  const { themeMode } = useTheme()
  return (
    <RouterLink
      to={to}
      onClick={onClick}
      className="nav-link-transition"
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 8,
        padding: '10px 14px',
        textDecoration: 'none',
        color: isActive ? tokens.colors.primary : token.colorText,
        background: isActive ? tokens.colors.activeNav[themeMode] : 'transparent',
        borderRadius: tokens.borderRadius.md,
        margin: '2px 8px',
        fontSize: 14,
        fontWeight: isActive ? 600 : 400,
        borderLeft: isActive ? `3px solid ${tokens.colors.primary}` : '3px solid transparent',
        paddingLeft: isActive ? 11 : 14,
      }}
    >
      <span style={{ fontSize: 18, minWidth: 20, textAlign: 'center' }}>{icon}</span>
      {!collapsed && label}
    </RouterLink>
  )
}

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
  const { t } = useTranslation()
  const renderLinks = () => (
    <>
      <nav>
        {navItems.filter(item =>
          !(item.adminOnly && !isAdmin) &&
          !(item.hideWhenNotificationsDisabled && user?.notifications_enabled === false) &&
          !(item.hideWhenRelayDisabled && user?.relay_ingestion_enabled !== true)
        ).map(item => (
          <NavLink
            key={item.key}
            to={item.key}
            isActive={location.pathname === item.key}
            onClick={onClose}
            icon={item.icon}
            label={t(item.labelKey)}
            collapsed={collapsed}
          />
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
              <NavLink
                key={`pin-${d.id}`}
                to={`/dashboards/${d.id}`}
                isActive={location.pathname === `/dashboards/${d.id}`}
                onClick={onClose}
                icon={<PushpinOutlined />}
                label={d.name}
                collapsed={collapsed}
              />
            ))}
          </>
        )}
      </nav>
    </>
  )

  return (
    <>
      <div style={{ padding: '20px 16px', fontSize: 20, fontWeight: 700, display: 'flex', alignItems: 'center', gap: 8 }}>
        <img src="/icons/icon-192.png" alt="Logmara" style={{ width: 28, height: 28, borderRadius: 6 }} />
        {!collapsed && <span className="gradient-text">Logmara</span>}
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

// LiveContext — shared live/polling state, surfaced by LiveIndicator in the header
const LiveContext = createContext<{ liveActive: boolean; setLiveActive: (v: boolean) => void }>({
  liveActive: false,
  setLiveActive: () => {},
})

export function useLive() {
  return useContext(LiveContext)
}

function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [themeMode, setThemeMode] = useState<ThemeMode>(() => {
    const saved = localStorage.getItem('syslog_theme')
    return (saved === 'dark' || saved === 'light') ? saved : 'light'
  })
  const [liveActive, setLiveActive] = useState(false)

  const toggleTheme = () => {
    setThemeMode(prev => {
      const next = prev === 'light' ? 'dark' : 'light'
      localStorage.setItem('syslog_theme', next)
      return next
    })
  }

  return (
    <ThemeContext.Provider value={{ themeMode, toggleTheme }}>
      <LiveContext.Provider value={{ liveActive, setLiveActive }}>
        <ConfigProvider theme={{
          algorithm: themeMode === 'dark' ? theme.darkAlgorithm : theme.defaultAlgorithm,
          token: {
            colorPrimary: tokens.colors.primary,
            colorError: tokens.colors.error,
            borderRadius: tokens.borderRadius.md,
            wireframe: false,
          },
          components: {
            Card: {
              boxShadow: tokens.shadow.card,
              colorBgContainer: themeMode === 'dark' ? '#1a1a1a' : '#ffffff',
            },
            Layout: {
              headerHeight: 48,
              siderBg: themeMode === 'dark' ? tokens.colors.sidebar.dark : tokens.colors.sidebar.light,
            },
          },
        }}>
          <style>{`
            html, body { margin: 0; padding: 0; height: 100%; width: 100%; }
            body { background: ${themeMode === 'dark' ? tokens.colors.background.dark : tokens.colors.background.light}; }
            .ant-message-error .anticon { color: ${tokens.colors.error} !important; }
            .ant-message-error .ant-message-notice-content { border-color: ${tokens.colors.error} !important; background: ${tokens.colors.errorBg} !important; }
            .ant-message-error { color: ${tokens.colors.error} !important; }
            @media (max-width: 768px) { .navbar-text-label { display: none !important; } }
          `}</style>
          {children}
        </ConfigProvider>
      </LiveContext.Provider>
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

  const getPageTitle = () => {
    if (location.pathname === '/') return t('nav.dashboard')
    const match = location.pathname.match(/^\/dashboards\/([^/]+)$/)?.[1]
    if (match && dashboardTitle) return `${t('nav.dashboards')} / ${dashboardTitle}`
    const key = `nav.${location.pathname.replace('/', '')}`
    return t(key) || (location.pathname.replace('/', '').charAt(0).toUpperCase() + location.pathname.slice(2) || 'Logmara')
  }

  return (
    <Layout style={{ minHeight: '100vh', background: token.colorBgContainer }}>
      <Layout>
        {!isMobile && (
          <Sider
            width={220}
            collapsedWidth={80}
            collapsed={collapsed}
            onCollapse={setCollapsed}
            style={{
              background: themeMode === 'dark' ? tokens.colors.sidebar.dark : tokens.colors.sidebar.light,
              transition: 'width 0.25s ease',
              overflow: 'hidden',
            }}
            theme={themeMode === 'dark' ? 'dark' : 'light'}
          >
            <NavContent
              location={location}
              user={user ?? undefined}
              logout={logout}
              isAdmin={isAdmin}
              pinnedDashboards={pinnedDashboards}
              loadingDashboards={loadingDashboards}
              collapsed={collapsed}
            />
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
          <Header
            style={{
              background: token.colorBgContainer,
              padding: '0 16px',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              borderBottom: `1px solid ${themeMode === 'dark' ? 'rgba(255,255,255,0.06)' : 'rgba(0,0,0,0.06)'}`,
            }}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
              {isMobile ? (
                <Button
                  type="text"
                  icon={<MenuOutlined />}
                  onClick={() => setDrawerVisible(true)}
                />
              ) : (
                <Button
                  type="text"
                  icon={<MenuOutlined />}
                  onClick={() => setCollapsed(!collapsed)}
                />
              )}
              <span style={{ fontSize: 16, fontWeight: 500 }}>{getPageTitle()}</span>
              <LiveIndicator />
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
          <Content
            className="animate-fade-in"
            style={{
              margin: isMobile ? 8 : 16,
              padding: isMobile ? 12 : 24,
               paddingBottom: isMobile ? 120 : 24,
              background: token.colorBgContainer,
              borderRadius: tokens.borderRadius.md,
            }}
          >
            <PasswordExpiryWarning />
            <ErrorBoundary>{children}</ErrorBoundary>
          </Content>
           {!isMobile && (
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
          )}
        </Layout>
      </Layout>
      {isMobile && (
        <Footer
          style={{
            position: 'fixed',
            bottom: `calc(56px + env(safe-area-inset-bottom, 0px))`,
            left: 0,
            right: 0,
            zIndex: 999,
            background: token.colorPrimaryBg,
            color: token.colorPrimaryText,
            fontSize: 12,
            padding: '10px 16px',
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
      )}
      {isMobile && <BottomNav />}
      {isMobile && <div className="bottom-safe-spacer" />}
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
  const { user, isAdmin, loading, refreshing } = useAuth()
  const location = useLocation()
  if (loading || refreshing) return <RouteFallback />
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
        <Typography.Text type="secondary" style={{ marginTop: 8 }}>System starting... Please wait</Typography.Text>
      </div>
    )
  }

  return (
    <I18nextProvider i18n={i18nInstance}>
      <ThemeProvider>
        <BrowserRouter>
          <Suspense fallback={<RouteFallback />}>
            {!initialized ? (
              <Routes>
                <Route path="/setup" element={<SetupWizard />} />
                <Route path="*" element={<Navigate to="/setup" replace />} />
              </Routes>
            ) : (
              <AuthProvider>
                <Routes>
                  <Route path="/login" element={<Login />} />
                  <Route path="/" element={<PrivateRoute><Dashboard /></PrivateRoute>} />
                  <Route path="/logs" element={<PrivateRoute><LogsViewer /></PrivateRoute>} />
                  <Route path="/parsers" element={<PrivateRoute><ParsersPage /></PrivateRoute>} />
                  <Route path="/dashboards" element={<PrivateRoute><DashboardsPage /></PrivateRoute>} />
                  <Route path="/dashboards/:id" element={<PrivateRoute><DashboardViewPage /></PrivateRoute>} />
                  <Route path="/alerts" element={<PrivateRoute><AlertsPage /></PrivateRoute>} />
                  <Route path="/admin" element={<PrivateRoute requireAdmin><Admin /></PrivateRoute>} />
                  <Route path="/relay" element={<PrivateRoute requireAdmin><SyslogRelay /></PrivateRoute>} />
                  <Route path="*" element={<PrivateRoute><NotFoundPage /></PrivateRoute>} />
                </Routes>
              </AuthProvider>
            )}
          </Suspense>
        </BrowserRouter>
      </ThemeProvider>
    </I18nextProvider>
  )
}
