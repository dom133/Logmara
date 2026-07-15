import { useEffect, useState, createContext, useContext } from 'react'
import { BrowserRouter, Routes, Route, Navigate, useLocation, Link as RouterLink } from 'react-router-dom'
import { Layout, theme, Spin, Result, ConfigProvider, Button, Drawer, Space } from 'antd'
import { DashboardOutlined, FileTextOutlined, SettingOutlined, FundOutlined, SafetyOutlined, PushpinOutlined, SunOutlined, MoonOutlined, MenuOutlined, LogoutOutlined, UserOutlined } from '@ant-design/icons'
import Login from './pages/Login'
import Dashboard from './pages/Dashboard'
import LogsViewer from './pages/LogsViewer'
import ParsersPage from './pages/Parsers'
import DashboardsPage from './pages/Dashboards'
import DashboardViewPage from './pages/DashboardView'

import Admin from './pages/Admin'
import SetupWizard from './pages/SetupWizard'
import ErrorBoundary from './components/ErrorBoundary'
import { AuthProvider, useAuth } from './services/auth'
import { getDashboards, Dashboard as DashboardType, checkInitialized } from './services/api'

const { Sider, Content, Header } = Layout

const MOBILE_BREAKPOINT = 768

function useIsMobile() {
  const [isMobile, setIsMobile] = useState(false)
  useEffect(() => {
    const m = window.matchMedia(`(max-width: ${MOBILE_BREAKPOINT}px)`)
    setIsMobile(m.matches)
    const handler = (e: MediaQueryListEvent) => setIsMobile(e.matches)
    m.addEventListener('change', handler)
    return () => m.removeEventListener('change', handler)
  }, [])
  return isMobile
}

function NavContent({ location, user, logout, isAdmin, pinnedDashboards, collapsed, onClose }: {
  location: ReturnType<typeof useLocation>
  user: { username?: string } | undefined
  logout: () => void
  isAdmin: boolean
  pinnedDashboards: DashboardType[]
  collapsed?: boolean
  onClose?: () => void
}) {
  const { token } = theme.useToken()
  const { themeMode } = useTheme()
  const activeBg = themeMode === 'dark' ? '#2a2a2a' : '#e6f7ff'
  const renderLinks = () => (
    <>
      <nav>
        {navItems.filter(item => !(item.adminOnly && !isAdmin)).map(item => (
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
            {!collapsed && item.label}
          </RouterLink>
        ))}
        {pinnedDashboards.length > 0 && (
          <>
            {!collapsed && (
            <div style={{ padding: '12px 16px 4px', fontSize: 11, color: token.colorTextTertiary, textTransform: 'uppercase', letterSpacing: 1 }}>
              Pinned
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
        <DashboardOutlined style={{ fontSize: 24 }} /> {!collapsed && 'SysLog GUI'}
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
  { key: '/', label: 'Dashboard', icon: <DashboardOutlined /> },
  { key: '/logs', label: 'Logs', icon: <FileTextOutlined /> },
  { key: '/parsers', label: 'Parsers', icon: <SettingOutlined /> },
  { key: '/dashboards', label: 'Dashboards', icon: <FundOutlined /> },
  { key: '/admin', label: 'Admin', icon: <SafetyOutlined />, adminOnly: true },
]

function AppLayout({ children }: { children: React.ReactNode }) {
  const { user, logout, isAdmin } = useAuth()
  const { token } = theme.useToken()
  const { themeMode, toggleTheme } = useTheme()
  const location = useLocation()
  const [pinnedDashboards, setPinnedDashboards] = useState<DashboardType[]>([])
  const [collapsed, setCollapsed] = useState(false)
  const [drawerVisible, setDrawerVisible] = useState(false)
  const isMobile = useIsMobile()

  useEffect(() => {
    if (!user) return
    const load = async () => {
      try {
        const dashboards = await getDashboards()
        setPinnedDashboards(dashboards.filter((d: DashboardType) => d.pinned))
      } catch { /* ignore */ }
    }
    load()
    const handler = () => load()
    window.addEventListener('dashboards-pinned-changed', handler)
    return () => window.removeEventListener('dashboards-pinned-changed', handler)
  }, [user])

  useEffect(() => {
    if (isMobile) {
      setCollapsed(true)
    }
  }, [isMobile])

  return (
    <Layout style={{ minHeight: '100vh', background: token.colorBgContainer }}>
      {!isMobile && (
        <Sider
          width={220}
          collapsedWidth={80}
          collapsed={collapsed}
          onCollapse={setCollapsed}
          style={{ background: token.colorBgContainer }}
          theme={themeMode === 'dark' ? 'dark' : 'light'}
        >
          <NavContent location={location} user={user ?? undefined} logout={logout} isAdmin={isAdmin} pinnedDashboards={pinnedDashboards} collapsed={collapsed} />
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
            <NavContent location={location} user={user ?? undefined} logout={logout} isAdmin={isAdmin} pinnedDashboards={pinnedDashboards} onClose={() => setDrawerVisible(false)} />
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
              {location.pathname === '/' ? 'Dashboard' : location.pathname.replace('/', '').charAt(0).toUpperCase() + location.pathname.slice(2) || 'SysLog GUI'}
            </span>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'nowrap', overflow: 'hidden' }}>
            <span style={{ fontSize: 13, color: token.colorTextSecondary, display: 'flex', alignItems: 'center', gap: 4, whiteSpace: 'nowrap' }}>
              <UserOutlined /> {user?.username}
            </span>
            <Button
              type="text"
              icon={themeMode === 'dark' ? <SunOutlined /> : <MoonOutlined />}
              onClick={toggleTheme}
            >
              <span className="navbar-text-label">{themeMode === 'dark' ? 'Light' : 'Dark'}</span>
            </Button>
            <Button
              type="text"
              danger
              icon={<LogoutOutlined />}
              onClick={logout}
            >
              <span className="navbar-text-label">Logout</span>
            </Button>
          </div>
        </Header>
        <Content style={{ margin: 16, padding: 24, background: token.colorBgContainer, borderRadius: 8 }}>
          <ErrorBoundary>{children}</ErrorBoundary>
        </Content>
      </Layout>
    </Layout>
  )
}

function PrivateRoute({ children }: { children: React.ReactNode }) {
  const { token } = useAuth()
  if (!token) return <Navigate to="/login" replace />
  return <AppLayout>{children}</AppLayout>
}

export default function App() {
  const [initialized, setInitialized] = useState<boolean | null>(null)

  useEffect(() => {
    const check = async () => {
      const res = await checkInitialized()
      setInitialized(res.initialized)
    }
    check()
  }, [])

  if (initialized === null) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '100vh' }}>
        <Spin size="large" />
      </div>
    )
  }

  return (
    <ThemeProvider>
      <BrowserRouter>
        <AuthProvider>
          <Routes>
          {!initialized ? (
            <>
              <Route path="/setup" element={<SetupWizard />} />
              <Route path="*" element={<Navigate to="/setup" replace />} />
            </>
          ) : (
            <>
              <Route path="/login" element={<Login />} />
              
              <Route path="/" element={<PrivateRoute><Dashboard /></PrivateRoute>} />
              <Route path="/logs" element={<PrivateRoute><LogsViewer /></PrivateRoute>} />
              <Route path="/parsers" element={<PrivateRoute><ParsersPage /></PrivateRoute>} />
              <Route path="/dashboards" element={<PrivateRoute><DashboardsPage /></PrivateRoute>} />
              <Route path="/dashboards/:id" element={<PrivateRoute><DashboardViewPage /></PrivateRoute>} />
              <Route path="/admin" element={<PrivateRoute><Admin /></PrivateRoute>} />
              <Route path="*" element={<PrivateRoute><Result status="404" title="404" subTitle="Page not found" /></PrivateRoute>} />
            </>
          )}
        </Routes>
        </AuthProvider>
      </BrowserRouter>
    </ThemeProvider>
  )
}