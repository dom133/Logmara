import { useEffect, useRef, useState } from 'react'
import { Badge, Button, Dropdown, Empty, List, Tag, Typography, theme } from 'antd'
import { BellOutlined } from '@ant-design/icons'
import { getNotifications, markNotificationsRead, streamNotifications, InAppNotification } from '../services/api'

const severityColor: Record<string, string> = {
  critical: 'red',
  error: 'volcano',
  warning: 'orange',
  info: 'blue',
}

function formatTime(iso: string) {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  return d.toLocaleString()
}

export function NotificationBell() {
  const { token } = theme.useToken()
  const [enabled, setEnabled] = useState(false)
  const [items, setItems] = useState<InAppNotification[]>([])
  const [unreadCount, setUnreadCount] = useState(0)
  const [open, setOpen] = useState(false)
  const lastIdRef = useRef(0)

  useEffect(() => {
    let cancelled = false
    let stopStream: (() => void) | null = null

    getNotifications().then(res => {
      if (cancelled) return
      setEnabled(res.enabled !== false)
      setItems(res.notifications || [])
      setUnreadCount(res.unread_count || 0)
      lastIdRef.current = res.last_id || 0

      if (res.enabled !== false) {
        stopStream = streamNotifications((n) => {
          setItems(prev => [n, ...prev].slice(0, 50))
          setUnreadCount(prev => prev + 1)
        })
      }
    }).catch(() => { /* ignore */ })

    return () => {
      cancelled = true
      stopStream?.()
    }
  }, [])

  if (!enabled) return null

  const handleOpenChange = (nextOpen: boolean) => {
    setOpen(nextOpen)
    if (nextOpen && unreadCount > 0) {
      const maxID = items.reduce((max, n) => Math.max(max, n.id), lastIdRef.current)
      lastIdRef.current = maxID
      markNotificationsRead(maxID).catch(() => { /* ignore */ })
      setUnreadCount(0)
    }
  }

  const handleClearAll = () => {
    markNotificationsRead(lastIdRef.current).catch(() => { /* ignore */ })
    setItems([])
    setUnreadCount(0)
  }

  const renderDropdown = () => (
    <div style={{
      width: 'min(360px, calc(100vw - 32px))',
      maxHeight: 420,
      overflowY: 'auto',
      background: token.colorBgElevated,
      borderRadius: token.borderRadiusLG,
      boxShadow: token.boxShadowSecondary,
    }}>
      <div style={{ padding: '10px 16px', borderBottom: `1px solid ${token.colorBorderSecondary}`, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Typography.Text strong>Notifications</Typography.Text>
        {items.length > 0 && <Button type="link" size="small" style={{ padding: 0 }} onClick={handleClearAll}>Clear all</Button>}
      </div>
      {items.length === 0 ? (
        <Empty description="No notifications" style={{ padding: 24 }} />
      ) : (
        <List
          dataSource={items}
          renderItem={(n) => (
            <List.Item style={{ padding: '10px 16px', display: 'block' }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', gap: 8 }}>
                <Typography.Text strong style={{ fontSize: 13 }}>{n.title}</Typography.Text>
                <Tag color={severityColor[n.severity] || 'default'} style={{ marginRight: 0 }}>{n.severity}</Tag>
              </div>
              <div style={{ fontSize: 13, color: token.colorTextSecondary, marginTop: 2 }}>{n.message}</div>
              <div style={{ fontSize: 11, color: token.colorTextTertiary, marginTop: 4 }}>{formatTime(n.created_at)}</div>
            </List.Item>
          )}
        />
      )}
    </div>
  )

  return (
    <Dropdown
      open={open}
      onOpenChange={handleOpenChange}
      trigger={['click']}
      placement="bottomRight"
      dropdownRender={renderDropdown}
    >
      <Button type="text" icon={
        <Badge count={unreadCount} size="small" offset={[-2, 2]}>
          <BellOutlined style={{ fontSize: 18 }} />
        </Badge>
      } />
    </Dropdown>
  )
}
