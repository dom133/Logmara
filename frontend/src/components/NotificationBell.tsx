import { useEffect, useRef, useState } from 'react'
import { Badge, Button, Dropdown, Empty, List, Tag, Typography } from 'antd'
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
  const [items, setItems] = useState<InAppNotification[]>([])
  const [unreadCount, setUnreadCount] = useState(0)
  const [open, setOpen] = useState(false)
  const lastIdRef = useRef(0)

  useEffect(() => {
    let cancelled = false

    getNotifications().then(res => {
      if (cancelled) return
      setItems(res.notifications || [])
      setUnreadCount(res.unread_count || 0)
      lastIdRef.current = res.last_id || 0
    }).catch(() => { /* ignore */ })

    const stop = streamNotifications((n) => {
      setItems(prev => [n, ...prev].slice(0, 50))
      setUnreadCount(prev => prev + 1)
    })

    return () => {
      cancelled = true
      stop()
    }
  }, [])

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
    <div style={{ width: 360, maxHeight: 420, overflowY: 'auto', background: 'var(--ant-color-bg-elevated, #fff)', borderRadius: 8, boxShadow: '0 6px 16px rgba(0,0,0,0.12)' }}>
      <div style={{ padding: '10px 16px', borderBottom: '1px solid rgba(0,0,0,0.06)', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
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
              <div style={{ fontSize: 13, color: 'rgba(0,0,0,0.65)', marginTop: 2 }}>{n.message}</div>
              <div style={{ fontSize: 11, color: 'rgba(0,0,0,0.45)', marginTop: 4 }}>{formatTime(n.created_at)}</div>
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
