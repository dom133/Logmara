import { useEffect, useState, createContext, useContext } from 'react'
import { BrowserRouter, Routes, Route, Navigate, useLocation, Link as RouterLink } from 'react-router-dom'
import { Layout, theme, Spin, Result, ConfigProvider, Button, Drawer } from 'antd'
import { DashboardOutlined, FileTextOutlined, SettingOutlined, FundOutlined, SafetyOutlined, PushpinOutlined, SunOutlined, MoonOutlined, MenuOutlined } from '@ant-design/icons'
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
  const renderLinks = () => (
    <>
      <nav>
        {navItems.map(item => (
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
              background: location.pathname === item.key ? '#e6f7ff' : 'transparent',
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
            <div style={{ padding: '12px 16px 4px', fontSize: 11, color: '#999', textTransform: 'uppercase', letterSpacing: 1 }}>
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
                  background: location.pathname === `/dashboards/${d.id}` ? '#e6f7ff' : 'transparent',
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
        {isAdmin && (
          <RouterLink
            to="/admin"
            onClick={onClose}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 8,
              padding: '12px 16px',
              textDecoration: 'none',
              color: location.pathname === '/admin' ? '#1890ff' : token.colorText,
              background: location.pathname === '/admin' ? '#e6f7ff' : 'transparent',
              borderRadius: 4,
              margin: '2px 8px',
              fontSize: 14,
            }}
          >
            <span style={{ fontSize: 18 }}><SafetyOutlined /></span>
            {!collapsed && 'Admin'}
          </RouterLink>
        )}
      </nav>
      <div style={{ position: 'absolute', bottom: 16, left: 16, right: 16 }}>
        {!collapsed && (
          <div style={{ fontSize: 12, color: '#888', marginBottom: 8 }}>
            {user?.username}
          </div>
        )}
        <button
          onClick={() => { logout(); onClose?.() }}
          style={{
            width: '100%',
            padding: '6px 0',
            border: '1px solid #d9d9d9',
            borderRadius: 4,
            background: 'white',
            cursor: 'pointer',
            fontSize: 12,
          }}
        >
          Logout
        </button>
      </div>
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
          .ant-message-error .anticon { color: #ff4d4f !important; }
          .ant-message-error .ant-message-notice-content { border-color: #ff4d4f !important; background: #fff2f0 !important; }
          .ant-message-error { color: #ff4d4f !important; }
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
    <Layout style={{ minHeight: '100vh' }}>
      {!isMobile && (
        <Sider
          width={220}
          collapsedWidth={80}
          collapsed={collapsed}
          onCollapse={setCollapsed}
          responsive
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
          width={[280, '85%']}
          styles={{ body: { padding: 0 } }}
        >
          <div style={{ background: token.colorBgContainer, height: '100%' }}>
            <NavContent location={location} user={user ?? undefined} logout={logout} isAdmin={isAdmin} pinnedDashboards={pinnedDashboards} onClose={() => setDrawerVisible(false)} />
          </div>
        </Drawer>
      )}
      <Layout>
        <Header style={{ background: token.colorBgContainer, padding: '0 16px', display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
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
          <Button
            type="text"
            icon={themeMode === 'dark' ? <SunOutlined /> : <MoonOutlined />}
            onClick={toggleTheme}
          >
            {themeMode === 'dark' ? 'Light' : 'Dark'}
          </Button>
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