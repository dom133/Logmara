import { useLocation, Link as RouterLink } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../services/auth'
import { useTheme } from '../App'
import { tokens } from '../theme/tokens'
import { DashboardOutlined, FileTextOutlined, FundOutlined, BellOutlined, SafetyOutlined } from '@ant-design/icons'

const NAV_ITEMS = [
  { key: '/', icon: <DashboardOutlined />, labelKey: 'nav.dashboard' },
  { key: '/logs', icon: <FileTextOutlined />, labelKey: 'nav.logs' },
  { key: '/dashboards', icon: <FundOutlined />, labelKey: 'nav.dashboards' },
  { key: '/alerts', icon: <BellOutlined />, labelKey: 'nav.alerts', hideWhenNotificationsDisabled: true },
  { key: '/admin', icon: <SafetyOutlined />, labelKey: 'nav.admin', adminOnly: true },
]

export default function BottomNav() {
  const location = useLocation()
  const { t } = useTranslation()
  const { isAdmin, user } = useAuth()
  const { themeMode } = useTheme()

  const visibleItems = NAV_ITEMS.filter(item =>
    !(item.adminOnly && !isAdmin) &&
    !(item.hideWhenNotificationsDisabled && user?.notifications_enabled === false),
  )

  const isDark = themeMode === 'dark'

  return (
    <nav
      style={{
        display: 'flex',
        justifyContent: 'space-around',
        alignItems: 'center',
        padding: '8px 0',
        paddingBottom: `calc(8px + env(safe-area-inset-bottom, 0px))`,
        background: isDark ? '#1f1f1f' : '#ffffff',
        borderTop: `1px solid ${isDark ? '#303030' : '#e8e8e8'}`,
        color: isDark ? '#ffffff' : '#000000',
      }}
    >
      {visibleItems.map(item => {
        const isActive = location.pathname === item.key ||
          (item.key !== '/' && location.pathname.startsWith(item.key))
        return (
          <RouterLink
            key={item.key}
            to={item.key}
            className="nav-link-transition"
            style={{
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'center',
              gap: 2,
              padding: '4px 8px',
              textDecoration: 'none',
              color: isActive ? tokens.colors.primary : (isDark ? 'rgba(255,255,255,0.65)' : 'rgba(0,0,0,0.65)'),
              fontSize: 10,
              fontWeight: isActive ? 600 : 400,
              minWidth: 48,
              minHeight: 48,
              justifyContent: 'center',
              borderRadius: tokens.borderRadius.md,
              background: isActive ? tokens.colors.activeNav[isDark ? 'dark' : 'light'] : 'transparent',
            }}
          >
            <span style={{ fontSize: 20 }}>{item.icon}</span>
            <span style={{ whiteSpace: 'nowrap' }}>{t(item.labelKey)}</span>
          </RouterLink>
        )
      })}
    </nav>
  )
}
