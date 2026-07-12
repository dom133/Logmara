import { useEffect, useState } from 'react'
import { BrowserRouter, Routes, Route, Navigate, useLocation, Link as RouterLink } from 'react-router-dom'
import { Layout, theme } from 'antd'
import Login from './pages/Login'
import Dashboard from './pages/Dashboard'
import LogsViewer from './pages/LogsViewer'
import ParsersPage from './pages/Parsers'
import DashboardsPage from './pages/Dashboards'
import DashboardViewPage from './pages/DashboardView'
import { AuthProvider, useAuth } from './services/auth'
import { getDashboards, Dashboard as DashboardType } from './services/api'

const { Sider, Content } = Layout

const navItems = [
  { key: '/', label: 'Main', icon: '📊' },
  { key: '/logs', label: 'Logs', icon: '📋' },
  { key: '/parsers', label: 'Parsers', icon: '⚙️' },
  { key: '/dashboards', label: 'Dashboards', icon: '📈' },
]

function AppLayout({ children }: { children: React.ReactNode }) {
  const { user, logout } = useAuth()
  const { token } = theme.useToken()
  const location = useLocation()
  const [pinnedDashboards, setPinnedDashboards] = useState<DashboardType[]>([])

  useEffect(() => {
    if (!user) return
    const load = async () => {
      try {
        const dashboards = await getDashboards()
        setPinnedDashboards(dashboards.filter((d: DashboardType) => d.pinned))
      } catch { /* ignore */ }
    }
    load()
  }, [user])

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Sider
        width={220}
        style={{ background: token.colorBgContainer }}
        theme="light"
      >
        <div style={{ padding: '20px 16px', fontSize: 20, fontWeight: 700, color: '#1890ff' }}>
          📡 SysLog GUI
        </div>
        <nav>
          {navItems.map(item => (
            <RouterLink
              key={item.key}
              to={item.key}
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
              <span>{item.icon}</span>
              {item.label}
            </RouterLink>
          ))}
          {pinnedDashboards.length > 0 && (
            <>
              <div style={{ padding: '12px 16px 4px', fontSize: 11, color: '#999', textTransform: 'uppercase', letterSpacing: 1 }}>
                Pinned
              </div>
              {pinnedDashboards.map(d => (
                <RouterLink
                  key={`pin-${d.id}`}
                  to={`/dashboards/${d.id}`}
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
                  <span>📌</span>
                  {d.name}
                </RouterLink>
              ))}
            </>
          )}
        </nav>
        <div style={{ position: 'absolute', bottom: 16, left: 16, right: 16 }}>
          <div style={{ fontSize: 12, color: '#888', marginBottom: 8 }}>
            {user?.username}
          </div>
          <button
            onClick={logout}
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
      </Sider>
      <Layout>
        <Content style={{ margin: 16, padding: 24, background: token.colorBgContainer, borderRadius: 8 }}>
          {children}
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
  return (
    <BrowserRouter>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="/" element={<PrivateRoute><Dashboard /></PrivateRoute>} />
          <Route path="/logs" element={<PrivateRoute><LogsViewer /></PrivateRoute>} />
          <Route path="/parsers" element={<PrivateRoute><ParsersPage /></PrivateRoute>} />
          <Route path="/dashboards" element={<PrivateRoute><DashboardsPage /></PrivateRoute>} />
          <Route path="/dashboards/:id" element={<PrivateRoute><DashboardViewPage /></PrivateRoute>} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </AuthProvider>
    </BrowserRouter>
  )
}